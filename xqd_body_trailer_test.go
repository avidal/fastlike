package fastlike

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type lateTrailerReader struct {
	response *http.Response
	sent     bool
}

func (r *lateTrailerReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "x"), nil
	}
	// net/http replaces a nil Trailer map when it discovers unannounced
	// trailers at EOF. This must be observed through the response itself rather
	// than through a retained copy of its old Trailer field.
	r.response.Trailer = http.Header{"X-Late": {"ready"}}
	return 0, io.EOF
}

func (*lateTrailerReader) Close() error { return nil }

func TestBodyTrailerValueGetReturnsRawValue(t *testing.T) {
	i := newBodyLengthTestInstance()
	handle, body := i.bodies.NewBuffer()
	body.trailersReady = true
	body.trailers = http.Header{"X-Trailer": {"value"}}
	nameAddr, nameSize := writeStr(t, i, 100, "X-Trailer")
	i.memory.Data()[205] = 0xa5

	if status := i.xqd_body_trailer_value_get(int32(handle), nameAddr, nameSize, 200, 5, 300); status != XqdStatusOK {
		t.Fatalf("trailer_value_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(300); got != 5 {
		t.Fatalf("nwritten = %d, want 5", got)
	}
	if got := string(i.memory.Data()[200:205]); got != "value" {
		t.Fatalf("value = %q, want %q", got, "value")
	}
	if got := i.memory.Data()[205]; got != 0xa5 {
		t.Fatalf("byte after value = %#x, want untouched %#x", got, byte(0xa5))
	}
}

func TestReaderBodyObservesTrailersPopulatedAtEOF(t *testing.T) {
	i := newBodyLengthTestInstance()
	response := &http.Response{}
	response.Body = &lateTrailerReader{response: response}
	handle, _ := i.bodies.NewResponseReader(response)

	if status := i.xqd_body_read(int32(handle), 100, 8, 200); status != XqdStatusOK {
		t.Fatalf("first body_read status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_read(int32(handle), 100, 8, 200); status != XqdStatusOK {
		t.Fatalf("EOF body_read status = %d, want %d", status, XqdStatusOK)
	}
	nameAddr, nameSize := writeStr(t, i, 300, "X-Late")
	if status := i.xqd_body_trailer_value_get(int32(handle), nameAddr, nameSize, 400, 16, 500); status != XqdStatusOK {
		t.Fatalf("trailer_value_get status = %d, want %d", status, XqdStatusOK)
	}
	if got := string(i.memory.Data()[400:405]); got != "ready" {
		t.Fatalf("late trailer value = %q, want %q", got, "ready")
	}
}

func TestUnannouncedResponseTrailerReachesDownstream(t *testing.T) {
	i := newOwnershipTestInstance()
	recorder := httptest.NewRecorder()
	i.ds_response = recorder
	response := &http.Response{}
	response.Body = &lateTrailerReader{response: response}
	bodyHandle, _ := i.bodies.NewResponseReader(response)
	responseHandle, downstreamResponse := i.responses.New()
	downstreamResponse.StatusCode = http.StatusOK

	if status := i.xqd_resp_send_downstream(int32(responseHandle), int32(bodyHandle), 0); status != XqdStatusOK {
		t.Fatalf("resp_send_downstream status = %d, want %d", status, XqdStatusOK)
	}
	result := recorder.Result()
	defer result.Body.Close()
	if _, err := io.ReadAll(result.Body); err != nil {
		t.Fatal(err)
	}
	if got := result.Trailer.Get("X-Late"); got != "ready" {
		t.Fatalf("downstream trailer = %q, want %q", got, "ready")
	}
}

func TestAbandonDownstreamBodyDoesNotPublishTrailers(t *testing.T) {
	i := newOwnershipTestInstance()
	recorder := httptest.NewRecorder()
	i.ds_response = recorder
	responseHandle, _ := i.responses.New()
	bodyHandle, body := i.bodies.NewBuffer()
	body.trailers = http.Header{"X-Incomplete": {"discard"}}

	if status := i.xqd_resp_send_downstream(int32(responseHandle), int32(bodyHandle), 1); status != XqdStatusOK {
		t.Fatalf("resp_send_downstream status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_abandon(int32(bodyHandle)); status != XqdStatusOK {
		t.Fatalf("body_abandon status = %d, want %d", status, XqdStatusOK)
	}
	if got := recorder.Header().Get(http.TrailerPrefix + "X-Incomplete"); got != "" {
		t.Fatalf("abandoned downstream trailer = %q, want absent", got)
	}
}

func TestBodyTrailerValueGetMissingReturnsInvalidArgument(t *testing.T) {
	i := newBodyLengthTestInstance()
	handle, body := i.bodies.NewBuffer()
	body.trailersReady = true
	body.trailers = http.Header{}
	nameAddr, nameSize := writeStr(t, i, 100, "X-Missing")

	if status := i.xqd_body_trailer_value_get(int32(handle), nameAddr, nameSize, 200, 64, 300); status != XqdErrInvalidArgument {
		t.Fatalf("trailer_value_get status = %d, want %d", status, XqdErrInvalidArgument)
	}
}

func TestBodyTrailerReadsRejectStreamingHandle(t *testing.T) {
	i := newBodyLengthTestInstance()
	handle, body := i.bodies.NewBuffer()
	body.isStreaming = true
	body.trailersReady = true
	nameAddr, nameSize := writeStr(t, i, 100, "X-Trailer")

	if status := i.xqd_body_read(int32(handle), 200, 64, 308); status != XqdErrInvalidHandle {
		t.Errorf("body_read status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_trailer_names_get(int32(handle), 200, 64, 0, 300, 308); status != XqdErrInvalidHandle {
		t.Errorf("trailer_names_get status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_trailer_value_get(int32(handle), nameAddr, nameSize, 200, 64, 308); status != XqdErrInvalidHandle {
		t.Errorf("trailer_value_get status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_trailer_values_get(int32(handle), nameAddr, nameSize, 200, 64, 0, 300, 308); status != XqdErrInvalidHandle {
		t.Errorf("trailer_values_get status = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestBodyTrailerAppendRejectsHeaderNameLimits(t *testing.T) {
	i := newBodyLengthTestInstance()
	handle, body := i.bodies.NewBuffer()
	nameAddr, nameSize := writeStr(t, i, 100, "X-Existing")
	valueAddr, valueSize := writeStr(t, i, 200, "value")

	if status := i.xqd_body_trailer_append(int32(handle), 1024, maxHTTPHeaderNameLen+1, valueAddr, valueSize); status != XqdErrInvalidArgument {
		t.Fatalf("oversized name status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if body.trailers != nil {
		t.Fatalf("oversized trailer name initialized trailers: %v", body.trailers)
	}

	body.trailers = headerMapAtNameLimit()
	if status := i.xqd_body_trailer_append(int32(handle), nameAddr, nameSize, valueAddr, valueSize); status != XqdErrInvalidArgument {
		t.Fatalf("name count limit status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if len(body.trailers) != maxHTTPHeaderNameCount {
		t.Fatalf("trailer count = %d, want %d", len(body.trailers), maxHTTPHeaderNameCount)
	}
	if got := body.trailers.Values("X-Existing"); len(got) != 1 || got[0] != "old" {
		t.Fatalf("X-Existing = %q, want [old]", got)
	}
}

func TestBodyTrailerAppendRejectsInvalidHeaderValue(t *testing.T) {
	i := newBodyLengthTestInstance()
	handle, body := i.bodies.NewBuffer()
	nameAddr, nameSize := writeStr(t, i, 100, "X-Trailer")
	valueAddr, valueSize := writeStr(t, i, 200, "bad\r\nvalue")

	if status := i.xqd_body_trailer_append(int32(handle), nameAddr, nameSize, valueAddr, valueSize); status != XqdErrInvalidArgument {
		t.Fatalf("body_trailer_append status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if len(body.trailers) != 0 {
		t.Fatalf("invalid trailer append mutated trailers: %v", body.trailers)
	}
}
