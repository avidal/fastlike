package fastlike

import (
	"context"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/bytecodealliance/wasmtime-go"
)

// Instance is an implementation of the XQD ABI along with a wasmtime.Instance configured to use it
// TODO: This has no public methods or public members. Should it even be public? The API could just
// be New and Fastlike.ServeHTTP(w, r)?
type Instance struct {
	wasmctx *wasmContext

	// This is used to get a memory handle and call the entrypoint function
	// Everything from here and below is reset on each incoming request
	wasm      *wasmtime.Instance
	interrupt *wasmtime.InterruptHandle
	memory    *Memory

	requests  *RequestHandles
	responses *ResponseHandles
	bodies    *BodyHandles

	// ds_request represents the downstream request, ie the one originated from the user agent
	ds_request *http.Request

	// ds_response represents the downstream response, where we're going to write the final output
	ds_response http.ResponseWriter

	// backends is used to issue subrequests
	backends BackendHandler

	// geobackend is a backend for geographic requests
	// these are issued by the guest when you attempt to lookup geo data
	geobackend Backend

	uaparser UserAgentParser

	log    *log.Logger
	abilog *log.Logger
}

// NewInstance returns an http.Handler that can handle a single request.
func NewInstance(wasmbytes []byte, opts ...InstanceOption) *Instance {
	var i = new(Instance)
	i.compile(wasmbytes)

	i.requests = &RequestHandles{}
	i.bodies = &BodyHandles{}
	i.responses = &ResponseHandles{}

	i.log = log.New(ioutil.Discard, "[fastlike] ", log.Lshortfile)
	i.abilog = log.New(ioutil.Discard, "[fastlike abi] ", log.Lshortfile)

	// By default, any subrequests will return a 502
	i.backends = defaultBackendHandler()

	// By default, all geo requests return the same data
	i.geobackend = GeoHandler(DefaultGeo)

	// By default, user agent parsing returns an empty useragent
	i.uaparser = func(_ string) UserAgent {
		return UserAgent{}
	}

	for _, o := range opts {
		o(i)
	}

	return i
}

func (i *Instance) reset() {
	// once i is done, drop everything off of it
	for _, r := range i.requests.handles {
		if r.Body != nil {
			r.Body.Close()
		}
	}
	for _, w := range i.responses.handles {
		if w.Body != nil {
			w.Body.Close()
		}
	}
	for _, b := range i.bodies.handles {
		if b.closer != nil {
			b.closer.Close()
		}
		if b.buf != nil {
			b.buf = nil
		}
	}

	// reset the handles, but we can reuse the already allocated space
	*i.requests = RequestHandles{}
	*i.responses = ResponseHandles{}
	*i.bodies = BodyHandles{}

	i.ds_response = nil
	i.ds_request = nil
	i.wasm = nil
	i.memory = nil
}

func (i *Instance) setup() {
	var err error
	i.wasm, err = i.wasmctx.linker.Instantiate(i.wasmctx.module)
	check(err)

	i.interrupt, err = i.wasmctx.store.InterruptHandle()
	check(err)

	i.memory = &Memory{&wasmMemory{mem: i.wasm.GetExport("memory").Memory()}}
}

// ServeHTTP serves the supplied request and response pair. This is not safe to call twice.
func (i *Instance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	i.setup()
	defer i.reset()

	var loops, ok = r.Header[http.CanonicalHeaderKey("cdn-loop")]
	if !ok {
		loops = []string{""}
	}

	var _, yeslog = r.Header[http.CanonicalHeaderKey("fastlike-verbose")]
	if yeslog {
		i.abilog.SetOutput(os.Stdout)
	}

	if strings.Contains(strings.Join(loops, "\x00"), "fastlike") {
		// immediately respond with a loop detection
		w.WriteHeader(http.StatusLoopDetected)
		w.Write([]byte("Loop detected! This request has already come through your fastly program."))
		w.Write([]byte("\n"))
		w.Write([]byte("You probably have a non-exhaustive backend handler?"))
		return
	}

	i.ds_request = r
	i.ds_response = w

	// Start a goroutine which will wait for the context to cancel or wait until the wasm calls are
	// complete
	donech := make(chan struct{}, 1)
	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
			// If the context cancels before we write to the donech it's a timeout/deadline/client
			// hung up and we should interrupt the wasm program.
			i.interrupt.Interrupt()
		case <-donech:
			// Otherwise, we're good and don't need to do anything else.
		}
	}(r.Context())

	// The entrypoint for a fastly compute program takes no arguments and returns nothing or an
	// error. The program itself is responsible for getting a handle on the downstream request
	// and sending a response downstream.
	entry := i.wasm.GetExport("_start").Func()
	_, err := entry.Call()
	donech <- struct{}{}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error running wasm program.\n"))
		w.Write([]byte("Below is a useless blob of wasm backtrace. There may be more in your server logs.\n"))
		w.Write([]byte(err.Error()))
		return
	}
}
