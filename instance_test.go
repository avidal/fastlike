package fastlike

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const wasmPageSize = 65536

func newWatInstance(t *testing.T, wat string) *Instance {
	t.Helper()
	return NewInstance(wat2wasm(t, wat))
}

func serveWat(t *testing.T, i *Instance, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	if r == nil {
		r = httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	}
	w := httptest.NewRecorder()
	i.ServeHTTP(w, r)
	return w
}

// servePost starts a POST in the background, which makes spinOnPostWat spin.
func servePost(t *testing.T, i *Instance, ctx context.Context) <-chan *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://example.com/", nil).WithContext(ctx)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serveWat(t, i, r) }()
	return done
}

func await(t *testing.T, done <-chan *httptest.ResponseRecorder, what string) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-done:
		return w
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not end", what)
		return nil
	}
}

func wantInterrupted(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "wasm trap: interrupt") {
		t.Fatalf("want an interrupted guest, got %d %q", w.Code, w.Body.String())
	}
}

// wantEmptyOK expects the response of a guest that sent none.
func wantEmptyOK(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("want an empty 200, got %d %q", w.Code, w.Body.String())
	}
}

// Each guest traps when a grow does not return what production would.
func TestInstanceLimitsMatchProduction(t *testing.T) {
	for name, wat := range map[string]string{
		"memory grows to 128 MiB and no further": fmt.Sprintf(`(module
  (memory (export "memory") 1)
  (func (export "_start")
    (if (i32.ne (memory.grow (i32.const %d)) (i32.const 1)) (then unreachable))
    (if (i32.ne (memory.grow (i32.const 1)) (i32.const -1)) (then unreachable))))`, maxWasmMemoryBytes/wasmPageSize-1),
		"two memories": `(module (memory (export "memory") 1) (memory 1) (func (export "_start")))`,
		"table grows to 100,000 elements and no further": fmt.Sprintf(`(module
  (memory (export "memory") 1)
  (table 1 funcref)
  (func (export "_start")
    (if (i32.ne (table.grow (ref.null func) (i32.const %d)) (i32.const 1)) (then unreachable))
    (if (i32.ne (table.grow (ref.null func) (i32.const 1)) (i32.const -1)) (then unreachable))))`, maxWasmTableElements-1),
	} {
		t.Run(name, func(t *testing.T) {
			wantEmptyOK(t, serveWat(t, newWatInstance(t, wat), nil))
		})
	}
}

// Modules that production refuses get a 500 on every request.
func TestInstanceRejectsModulesOverProductionLimits(t *testing.T) {
	const start = `(func (export "_start"))`
	for name, wat := range map[string]string{
		"three memories":        `(module (memory (export "memory") 1) (memory 1) (memory 1) ` + start + `)`,
		"two tables":            `(module (memory (export "memory") 1) (table 1 funcref) (table 1 funcref) ` + start + `)`,
		"memory over 128 MiB":   fmt.Sprintf(`(module (memory (export "memory") %d) %s)`, maxWasmMemoryBytes/wasmPageSize+1, start),
		"table over 100k":       fmt.Sprintf(`(module (memory (export "memory") 1) (table %d funcref) %s)`, maxWasmTableElements+1, start),
		"no _start":             `(module (memory (export "memory") 1))`,
		"no memory":             `(module ` + start + `)`,
		"start is not function": `(module (memory (export "memory") 1) (global (export "_start") i32 (i32.const 0)))`,
	} {
		t.Run(name, func(t *testing.T) {
			i := newWatInstance(t, wat)
			for n := 0; n < 2; n++ {
				w := serveWat(t, i, nil)
				if w.Code != http.StatusInternalServerError || !strings.HasPrefix(w.Body.String(), "Error instantiating wasm program.") {
					t.Fatalf("request %d: got %d %q", n, w.Code, w.Body.String())
				}
			}
		})
	}
}

type claimSpy struct{ claimed bool }

func (c *claimSpy) claimOriginalHeaderNames(*http.Request) []string {
	c.claimed = true
	return nil
}

// A failed setup must still claim the header names, or the next request would get them.
func TestInstanceClaimsHeaderNamesWhenSetupFails(t *testing.T) {
	i := newWatInstance(t, `(module (memory (export "memory") 1) (table 1 funcref) (table 1 funcref) (func (export "_start")))`)
	spy := &claimSpy{}
	r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	r = r.WithContext(context.WithValue(r.Context(), originalHeadersCtxKey{}, spy))

	if w := serveWat(t, i, r); w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !spy.claimed {
		t.Fatal("the header names were left for the next request")
	}
}

// A POST makes this guest spin forever without calling the host, anything else returns at once.
const spinOnPostWat = `(module
  (import "fastly_http_req" "body_downstream_get" (func $downstream (param i32 i32) (result i32)))
  (import "fastly_http_req" "method_get" (func $method (param i32 i32 i32 i32) (result i32)))
  (memory (export "memory") 1)
  (func (export "_start")
    (drop (call $downstream (i32.const 0) (i32.const 4)))
    (drop (call $method (i32.load (i32.const 0)) (i32.const 16) (i32.const 16) (i32.const 8)))
    (if (i32.eq (i32.load8_u (i32.const 16)) (i32.const 80))
      (then (loop $spin (br $spin))))))`

func TestInstanceInterruptsSpinningGuestOnCancel(t *testing.T) {
	i := newWatInstance(t, spinOnPostWat)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	wantInterrupted(t, await(t, servePost(t, i, ctx), "the spinning guest"))

	// The interrupt must not leak into the next request served by the same instance.
	for n := 0; n < 3; n++ {
		wantEmptyOK(t, serveWat(t, i, nil))
	}
}

func TestInstanceInterruptsGuestWhoseRequestIsAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wantInterrupted(t, await(t, servePost(t, newWatInstance(t, spinOnPostWat), ctx), "the spinning guest"))
}

// A guest can return before the interrupt of its cancelled request lands.
// That interrupt must not hit the next request.
func TestInstanceInterruptDoesNotLeakIntoNextRequest(t *testing.T) {
	i := newWatInstance(t, spinOnPostWat)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for n := 0; n < 500; n++ {
		serveWat(t, i, httptest.NewRequest(http.MethodGet, "http://example.com/", nil).WithContext(cancelled))
		wantEmptyOK(t, serveWat(t, i, nil))
	}
}

// Cancelling one request must not interrupt another one running the same module.
func TestInstanceInterruptOnlyReachesItsOwnRequest(t *testing.T) {
	wasmfile := filepath.Join(t.TempDir(), "spin.wasm")
	if err := os.WriteFile(wasmfile, wat2wasm(t, spinOnPostWat), 0o644); err != nil {
		t.Fatal(err)
	}
	f := New(wasmfile)
	defer f.Close()

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	first := servePost(t, f.Instantiate(), firstCtx)
	second := servePost(t, f.Instantiate(), secondCtx)

	time.Sleep(20 * time.Millisecond)
	cancelFirst()
	wantInterrupted(t, await(t, first, "the first request"))

	select {
	case w := <-second:
		t.Fatalf("the second request ended when the first one was cancelled: %d %q", w.Code, w.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
	cancelSecond()
	wantInterrupted(t, await(t, second, "the second request"))
}
