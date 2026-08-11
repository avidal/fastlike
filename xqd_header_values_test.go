package fastlike

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"slices"
	"testing"
)

func newHeaderValuesTestInstance() *Instance {
	return &Instance{
		requests:  &RequestHandles{},
		responses: &ResponseHandles{},
		memory:    &Memory{ByteMemory(make([]byte, 1024))},
		abilog:    log.New(io.Discard, "", 0),
	}
}

func TestReqHeaderValuesGetPreservesValueOrder(t *testing.T) {
	i := newHeaderValuesTestInstance()
	handle, req := i.requests.New()
	want := []string{"z-first", "a-later"}
	req.Header["X-Order"] = slices.Clone(want)

	nameAddr, nameSize := writeStr(t, i, 100, "X-Order")
	if status := i.xqd_req_header_values_get(int32(handle), nameAddr, nameSize, 200, int32(len(want[0])+1), 0, 300, 308); status != XqdStatusOK {
		t.Fatalf("header_values_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := string(i.memory.Data()[200 : 200+i.memory.Uint32(308)]); got != want[0]+"\x00" {
		t.Fatalf("first iterated value = %q, want %q", got, want[0]+"\x00")
	}
	if got := req.Header.Values("X-Order"); !slices.Equal(got, want) {
		t.Fatalf("stored values after header_values_get = %q, want %q", got, want)
	}

	if status := i.xqd_req_header_value_get(int32(handle), nameAddr, nameSize, 400, 64, 500); status != XqdStatusOK {
		t.Fatalf("header_value_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := string(i.memory.Data()[400 : 400+i.memory.Uint32(500)]); got != want[0] {
		t.Fatalf("header_value_get after iteration = %q, want %q", got, want[0])
	}
}

func TestRespHeaderValuesGetPreservesValueOrder(t *testing.T) {
	i := newHeaderValuesTestInstance()
	handle, resp := i.responses.New()
	want := []string{"z-first", "a-later"}
	resp.Header = http.Header{"X-Order": slices.Clone(want)}

	nameAddr, nameSize := writeStr(t, i, 100, "X-Order")
	if status := i.xqd_resp_header_values_get(int32(handle), nameAddr, nameSize, 200, int32(len(want[0])+1), 0, 300, 308); status != XqdStatusOK {
		t.Fatalf("header_values_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := string(i.memory.Data()[200 : 200+i.memory.Uint32(308)]); got != want[0]+"\x00" {
		t.Fatalf("first iterated value = %q, want %q", got, want[0]+"\x00")
	}
	if got := resp.Header.Values("X-Order"); !slices.Equal(got, want) {
		t.Fatalf("stored values after header_values_get = %q, want %q", got, want)
	}

	if status := i.xqd_resp_header_value_get(int32(handle), nameAddr, nameSize, 400, 64, 500); status != XqdStatusOK {
		t.Fatalf("header_value_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := string(i.memory.Data()[400 : 400+i.memory.Uint32(500)]); got != want[0] {
		t.Fatalf("header_value_get after iteration = %q, want %q", got, want[0])
	}
}

func TestRespHeaderValueGetMissingReturnsInvalidArgument(t *testing.T) {
	i := newHeaderValuesTestInstance()
	handle, resp := i.responses.New()
	resp.Header = http.Header{}
	nameAddr, nameSize := writeStr(t, i, 100, "X-Missing")
	i.memory.PutUint32(0xdeadbeef, 300)

	if status := i.xqd_resp_header_value_get(int32(handle), nameAddr, nameSize, 200, 64, 300); status != XqdErrInvalidArgument {
		t.Fatalf("header_value_get status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if got := i.memory.Uint32(300); got != 0xdeadbeef {
		t.Fatalf("missing response header changed nwritten to %#x", got)
	}
}

func TestReqHeaderValueGetMissingLeavesOutputUntouched(t *testing.T) {
	i := newHeaderValuesTestInstance()
	handle, _ := i.requests.New()
	nameAddr, nameSize := writeStr(t, i, 100, "X-Missing")
	i.memory.PutUint32(0xdeadbeef, 300)

	if status := i.xqd_req_header_value_get(int32(handle), nameAddr, nameSize, 200, 64, 300); status != XqdErrInvalidArgument {
		t.Fatalf("header_value_get status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if got := i.memory.Uint32(300); got != 0xdeadbeef {
		t.Fatalf("missing request header changed nwritten to %#x", got)
	}
}

func TestHeaderRemoveAcceptsPresentEmptyValue(t *testing.T) {
	i := newHeaderValuesTestInstance()
	nameAddr, nameSize := writeStr(t, i, 100, "X-Empty")

	reqHandle, req := i.requests.New()
	req.Header["X-Empty"] = []string{""}
	if status := i.xqd_req_header_remove(int32(reqHandle), nameAddr, nameSize); status != XqdStatusOK {
		t.Fatalf("request header_remove status = %d, want %d", status, XqdStatusOK)
	}
	if _, exists := req.Header["X-Empty"]; exists {
		t.Fatal("request header_remove left the empty-valued header present")
	}

	respHandle, resp := i.responses.New()
	resp.Header = http.Header{"X-Empty": {""}}
	if status := i.xqd_resp_header_remove(int32(respHandle), nameAddr, nameSize); status != XqdStatusOK {
		t.Fatalf("response header_remove status = %d, want %d", status, XqdStatusOK)
	}
	if _, exists := resp.Header["X-Empty"]; exists {
		t.Fatal("response header_remove left the empty-valued header present")
	}
}

func TestReqMethodSetAcceptsAndPreservesCustomMethod(t *testing.T) {
	i := newHeaderValuesTestInstance()
	handle, req := i.requests.New()
	methodAddr, methodSize := writeStr(t, i, 100, "pUrGe")

	if status := i.xqd_req_method_set(int32(handle), methodAddr, methodSize); status != XqdStatusOK {
		t.Fatalf("method_set status = %d, want %d", status, XqdStatusOK)
	}
	if req.Method != "pUrGe" {
		t.Fatalf("stored method = %q, want exact input %q", req.Method, "pUrGe")
	}
}

func TestReqMethodSetRejectsEmptyMethod(t *testing.T) {
	i := newHeaderValuesTestInstance()
	handle, _ := i.requests.New()

	if status := i.xqd_req_method_set(int32(handle), 0, 0); status != XqdErrInvalidArgument {
		t.Fatalf("empty method_set status = %d, want %d", status, XqdErrInvalidArgument)
	}
}

func TestReqMethodSetRejectsNonTokenMethod(t *testing.T) {
	i := newHeaderValuesTestInstance()
	handle, _ := i.requests.New()
	methodAddr, methodSize := writeStr(t, i, 100, "NOT VALID")

	if status := i.xqd_req_method_set(int32(handle), methodAddr, methodSize); status != XqdErrInvalidArgument {
		t.Fatalf("invalid method_set status = %d, want %d", status, XqdErrInvalidArgument)
	}
}

func TestReqMethodSetDoesNotApplyHeaderNameLengthLimit(t *testing.T) {
	i := newHeaderValuesTestInstance()
	i.memory = &Memory{ByteMemory(make([]byte, maxHTTPHeaderNameLen+64))}
	handle, req := i.requests.New()
	method := bytes.Repeat([]byte{'A'}, int(maxHTTPHeaderNameLen)+1)
	if _, err := i.memory.WriteAt(method, 0); err != nil {
		t.Fatal(err)
	}

	if status := i.xqd_req_method_set(int32(handle), 0, int32(len(method))); status != XqdStatusOK {
		t.Fatalf("method_set status = %d, want %d", status, XqdStatusOK)
	}
	if req.Method != string(method) {
		t.Fatal("method_set did not preserve the long valid token")
	}
}

func TestHeaderNamesOverRuntimeLimitAreRejectedBeforeMemoryRead(t *testing.T) {
	i := newHeaderValuesTestInstance()
	reqHandle, _ := i.requests.New()
	respHandle, _ := i.responses.New()
	overLimit := maxHTTPHeaderNameLen + 1

	if status := i.xqd_req_header_value_get(int32(reqHandle), 1024, overLimit, 0, 0, 0); status != XqdErrInvalidArgument {
		t.Fatalf("request header status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if status := i.xqd_resp_header_values_get(int32(respHandle), 1024, overLimit, 0, 0, 0, 0, 0); status != XqdErrInvalidArgument {
		t.Fatalf("response header status = %d, want %d", status, XqdErrInvalidArgument)
	}
}

func TestHeaderValuesSetEmptyClearsHeader(t *testing.T) {
	i := newHeaderValuesTestInstance()
	nameAddr, nameSize := writeStr(t, i, 100, "X-Clear")

	reqHandle, req := i.requests.New()
	req.Header["X-Clear"] = []string{"old"}
	if status := i.xqd_req_header_values_set(int32(reqHandle), nameAddr, nameSize, 0, 0); status != XqdStatusOK {
		t.Fatalf("request header_values_set status = %d, want %d", status, XqdStatusOK)
	}
	if _, exists := req.Header["X-Clear"]; exists {
		t.Fatal("request header_values_set with an empty list left the header present")
	}

	respHandle, resp := i.responses.New()
	resp.Header = http.Header{"X-Clear": {"old"}}
	if status := i.xqd_resp_header_values_set(int32(respHandle), nameAddr, nameSize, 0, 0); status != XqdStatusOK {
		t.Fatalf("response header_values_set status = %d, want %d", status, XqdStatusOK)
	}
	if _, exists := resp.Header["X-Clear"]; exists {
		t.Fatal("response header_values_set with an empty list left the header present")
	}
}

func TestHeaderOperationsRejectInvalidSyntaxWithoutMutation(t *testing.T) {
	i := newHeaderValuesTestInstance()
	reqHandle, req := i.requests.New()
	respHandle, resp := i.responses.New()
	resp.Header = http.Header{}

	badNameAddr, badNameSize := writeStr(t, i, 100, "Bad Name")
	valueAddr, valueSize := writeStr(t, i, 200, "value")
	if status := i.xqd_req_header_insert(int32(reqHandle), badNameAddr, badNameSize, valueAddr, valueSize); status != XqdErrInvalidArgument {
		t.Fatalf("request header_insert with invalid name status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if len(req.Header) != 0 {
		t.Fatalf("invalid request header_insert mutated headers: %v", req.Header)
	}
	if status := i.xqd_req_header_values_get(int32(reqHandle), badNameAddr, badNameSize, 300, 64, 0, 400, 408); status != XqdErrInvalidArgument {
		t.Fatalf("request header_values_get with invalid name status = %d, want %d", status, XqdErrInvalidArgument)
	}

	goodNameAddr, goodNameSize := writeStr(t, i, 500, "X-Good")
	badValueAddr, badValueSize := writeStr(t, i, 600, "bad\nvalue")
	if status := i.xqd_resp_header_append(int32(respHandle), goodNameAddr, goodNameSize, badValueAddr, badValueSize); status != XqdErrInvalidArgument {
		t.Fatalf("response header_append with invalid value status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if len(resp.Header) != 0 {
		t.Fatalf("invalid response header_append mutated headers: %v", resp.Header)
	}

	req.Header["X-Good"] = []string{"old"}
	valuesAddr, valuesSize := writeStr(t, i, 700, "good\x00bad\nvalue\x00")
	if status := i.xqd_req_header_values_set(int32(reqHandle), goodNameAddr, goodNameSize, valuesAddr, valuesSize); status != XqdErrInvalidArgument {
		t.Fatalf("request header_values_set with invalid value status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if got := req.Header.Values("X-Good"); !slices.Equal(got, []string{"old"}) {
		t.Fatalf("invalid header_values_set changed values to %q, want [old]", got)
	}
}

func TestHTTPVersionSetAcceptsH2AndH3(t *testing.T) {
	i := newHeaderValuesTestInstance()
	reqHandle, _ := i.requests.New()
	respHandle, _ := i.responses.New()

	if status := i.xqd_req_version_set(int32(reqHandle), Http2); status != XqdStatusOK {
		t.Fatalf("request version_set(H2) status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_resp_version_set(int32(respHandle), Http3); status != XqdStatusOK {
		t.Fatalf("response version_set(H3) status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_req_version_get(int32(reqHandle), 100); status != XqdStatusOK {
		t.Fatalf("request version_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(100); got != uint32(Http2) {
		t.Fatalf("request version = %d, want %d", got, Http2)
	}
	if status := i.xqd_resp_version_get(int32(respHandle), 104); status != XqdStatusOK {
		t.Fatalf("response version_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(104); got != uint32(Http3) {
		t.Fatalf("response version = %d, want %d", got, Http3)
	}
}
