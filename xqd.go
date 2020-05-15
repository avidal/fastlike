package fastlike

import (
	"fmt"
	"io"
	"strings"

	"github.com/ua-parser/uap-go/uaparser"
)

func (i *Instance) xqd_init(abiv int64) XqdStatus {
	fmt.Printf("xqd_init, abiv=%d\n", abiv)
	if abiv != 1 {
		return XqdErrUnsupported
	}

	return XqdStatusOK
}

func (i *Instance) xqd_req_body_downstream_get(request_handle_out int32, body_handle_out int32) XqdStatus {
	fmt.Printf("xqd_req_body_downstream_get, rh=%d, bh=%d\n", request_handle_out, body_handle_out)

	// Convert the downstream request into a (request, body) handle pair
	var rhid, rh = i.requests.New()
	rh.Request = i.ds_request

	// downstream requests don't carry host or scheme on their url for some dumb reason
	rh.URL.Host = rh.Host
	rh.URL.Scheme = "http"
	if rh.TLS != nil {
		rh.URL.Scheme = "https"
	}

	var bhid, _ = i.bodies.NewReader(rh.Body)

	i.memory.PutUint32(uint32(rhid), int64(request_handle_out))
	i.memory.PutUint32(uint32(bhid), int64(body_handle_out))

	return XqdStatusOK
}

func (i *Instance) xqd_resp_send_downstream(whandle int32, bhandle int32, stream int32) XqdStatus {
	fmt.Printf("xqd_resp_send_downstream, wh=%d, bh=%d, stream=%d\n", whandle, bhandle, stream)
	if stream != 0 {
		return XqdErrUnsupported
	}

	var w, b = i.responses.Get(int(whandle)), i.bodies.Get(int(bhandle))
	if w == nil || b == nil {
		return XqdErrInvalidHandle
	}
	defer b.Close()

	fmt.Printf("\trw=%v, w=%v, b=%+v\n", i.ds_response, w, b)

	for k, v := range w.Header {
		i.ds_response.Header()[k] = v
	}

	i.ds_response.WriteHeader(w.StatusCode)

	_, err := io.Copy(i.ds_response, b)
	if err != nil {
		return XqdError
	}

	return XqdStatusOK
}

// TODO: Bring in `tobie/ua-parser/go/uaparser` for this?
func (i *Instance) xqd_uap_parse(
	addr int32, size int32,
	family_out, family_maxlen, family_nwritten_out int32,
	major_out, major_maxlen, major_nwritten_out int32,
	minor_out, minor_maxlen, minor_nwritten_out int32,
	patch_out, patch_maxlen, patch_nwritten_out int32,
) XqdStatus {
	fmt.Printf("xqd_uap_parse, addr=%d, size=%d\n", addr, size)

	var buf = make([]byte, size)
	_, err := i.memory.ReadAt(buf, int64(addr))
	if err != nil {
		return XqdError
	}

	var useragent = string(buf)
	fmt.Printf("\tparsing ua %q\n", useragent)

	var parser = uaparser.NewFromSaved()
	var ua = parser.ParseUserAgent(useragent)

	family_nwritten, err := i.memory.WriteAt([]byte(ua.Family), int64(family_out))
	if err != nil {
		return XqdError
	}
	i.memory.PutUint32(uint32(family_nwritten), int64(family_nwritten_out))

	major_nwritten, err := i.memory.WriteAt([]byte(ua.Major), int64(major_out))
	if err != nil {
		return XqdError
	}
	i.memory.PutUint32(uint32(major_nwritten), int64(major_nwritten_out))

	minor_nwritten, err := i.memory.WriteAt([]byte(ua.Minor), int64(minor_out))
	if err != nil {
		return XqdError
	}
	i.memory.PutUint32(uint32(minor_nwritten), int64(minor_nwritten_out))

	patch_nwritten, err := i.memory.WriteAt([]byte(ua.Patch), int64(patch_out))
	if err != nil {
		return XqdError
	}
	i.memory.PutUint32(uint32(patch_nwritten), int64(patch_nwritten_out))

	return XqdStatusOK
}

func p(name string, args ...int32) {
	xs := []string{}
	for _, x := range args {
		xs = append(xs, fmt.Sprintf("%d", x))
	}

	fmt.Printf(":STUB: %s with %s\n", name, strings.Join(xs, ", "))
}

func (i *Instance) wasm0(name string) func() int32 {
	return func() int32 {
		p(name)
		return 5
	}
}

func (i *Instance) wasm1(name string) func(a int32) int32 {
	return func(a int32) int32 {
		p(name, a)
		return 5
	}
}

func (i *Instance) wasm2(name string) func(a, b int32) int32 {
	return func(a, b int32) int32 {
		p(name, a, b)
		return 5
	}
}

func (i *Instance) wasm3(name string) func(a, b, c int32) int32 {
	return func(a, b, c int32) int32 {
		p(name, a, b, c)
		return 5
	}
}

func (i *Instance) wasm4(name string) func(a, b, c, d int32) int32 {
	return func(a, b, c, d int32) int32 {
		p(name, a, b, c, d)
		return 5
	}
}

func (i *Instance) wasm5(name string) func(a, b, c, d, e int32) int32 {
	return func(a, b, c, d, e int32) int32 {
		p(name, a, b, c, d, e)
		return 5
	}
}

func (i *Instance) wasm6(name string) func(a, b, c, d, e, f int32) int32 {
	return func(a, b, c, d, e, f int32) int32 {
		p(name, a, b, c, d, e, f)
		return 5
	}
}

func (i *Instance) wasm7(name string) func(a, b, c, d, e, f, g int32) int32 {
	return func(a, b, c, d, e, f, g int32) int32 {
		p(name, a, b, c, d, e, f, g)
		return 5
	}
}

func (i *Instance) wasm8(name string) func(a, b, c, d, e, f, g, h int32) int32 {
	return func(a, b, c, d, e, f, g, h int32) int32 {
		p(name, a, b, c, d, e, f, g, h)
		return 5
	}
}
