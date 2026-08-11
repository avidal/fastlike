package fastlike

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newOwnershipTestInstance() *Instance {
	return &Instance{
		requests:        &RequestHandles{},
		responses:       &ResponseHandles{},
		bodies:          &BodyHandles{},
		pendingRequests: &PendingRequestHandles{},
		requestPromises: &RequestPromiseHandles{},
		backends:        map[string]*Backend{},
		memory:          &Memory{ByteMemory(make([]byte, 2048))},
		abilog:          log.New(io.Discard, "", 0),
		ds_context:      context.Background(),
	}
}

func addNoContentBackend(i *Instance) {
	i.addBackend("origin", &Backend{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})})
}

func TestRequestSendConsumesRequestAndBodyHandles(t *testing.T) {
	i := newOwnershipTestInstance()
	addNoContentBackend(i)
	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	backendAddr, backendSize := writeStr(t, i, 100, "origin")

	if status := i.xqd_req_send(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 200, 204); status != XqdStatusOK {
		t.Fatalf("req_send status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_req_version_get(int32(reqHandle), 300); status != XqdErrInvalidHandle {
		t.Fatalf("request handle after send status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_known_length(int32(bodyHandle), 304); status != XqdErrInvalidHandle {
		t.Fatalf("body handle after send status = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestAsyncRequestSendConsumesNonStreamingHandles(t *testing.T) {
	i := newOwnershipTestInstance()
	addNoContentBackend(i)
	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	backendAddr, backendSize := writeStr(t, i, 100, "origin")

	if status := i.xqd_req_send_async(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 200); status != XqdStatusOK {
		t.Fatalf("req_send_async status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_req_version_get(int32(reqHandle), 300); status != XqdErrInvalidHandle {
		t.Fatalf("request handle after async send status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_known_length(int32(bodyHandle), 304); status != XqdErrInvalidHandle {
		t.Fatalf("body handle after async send status = %d, want %d", status, XqdErrInvalidHandle)
	}
	pending := i.pendingRequests.Get(int(i.memory.Uint32(200)))
	select {
	case <-pending.done:
	case <-time.After(time.Second):
		t.Fatal("async request did not complete")
	}
}

func TestStreamingRequestSendConsumesOnlyRequestHandle(t *testing.T) {
	i := newOwnershipTestInstance()
	addNoContentBackend(i)
	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	backendAddr, backendSize := writeStr(t, i, 100, "origin")

	if status := i.xqd_req_send_async_streaming(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 200); status != XqdStatusOK {
		t.Fatalf("req_send_async_streaming status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_req_version_get(int32(reqHandle), 300); status != XqdErrInvalidHandle {
		t.Fatalf("request handle after streaming send status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_known_length(int32(bodyHandle), 304); status != XqdErrNone {
		t.Fatalf("streaming body handle status = %d, want valid handle with unknown length (%d)", status, XqdErrNone)
	}
	if status := i.xqd_body_close(int32(bodyHandle)); status != XqdStatusOK {
		t.Fatalf("body_close status = %d, want %d", status, XqdStatusOK)
	}
	pending := i.pendingRequests.Get(int(i.memory.Uint32(200)))
	select {
	case <-pending.done:
	case <-time.After(time.Second):
		t.Fatal("streaming request did not complete")
	}
}

func TestRequestSendsRejectAlreadyStreamingBody(t *testing.T) {
	tests := []struct {
		name string
		send func(*Instance, int32, int32, int32, int32) int32
	}{
		{
			name: "sync",
			send: func(i *Instance, request, body, backendAddr, backendSize int32) int32 {
				return i.xqd_req_send(request, body, backendAddr, backendSize, 300, 304)
			},
		},
		{
			name: "async",
			send: func(i *Instance, request, body, backendAddr, backendSize int32) int32 {
				return i.xqd_req_send_async(request, body, backendAddr, backendSize, 300)
			},
		},
		{
			name: "async_streaming",
			send: func(i *Instance, request, body, backendAddr, backendSize int32) int32 {
				return i.xqd_req_send_async_streaming(request, body, backendAddr, backendSize, 300)
			},
		},
		{
			name: "v2",
			send: func(i *Instance, request, body, backendAddr, backendSize int32) int32 {
				return i.xqd_req_send_v2(request, body, backendAddr, backendSize, 320, 340, 344)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			i := newOwnershipTestInstance()
			requestID, _ := i.requests.New()
			bodyID, body := i.bodies.NewBuffer()
			body.isStreaming = true
			body.streamingChan = make(chan []byte, 1)
			body.streamingDone = make(chan struct{})
			body.streamingAbandon = make(chan struct{})
			backendAddr, backendSize := writeStr(t, i, 100, "origin")

			if status := test.send(i, int32(requestID), int32(bodyID), backendAddr, backendSize); status != XqdErrInvalidHandle {
				t.Fatalf("send status = %d, want %d", status, XqdErrInvalidHandle)
			}
			if i.requests.Get(requestID) != nil {
				t.Fatal("request handle was not consumed before the body error")
			}
			if got := i.bodies.Get(bodyID); got != body {
				t.Fatal("rejected streaming body handle was consumed")
			}
			if status := i.xqd_body_abandon(int32(bodyID)); status != XqdStatusOK {
				t.Fatalf("body_abandon status = %d, want %d", status, XqdStatusOK)
			}

			i = newOwnershipTestInstance()
			requestID, _ = i.requests.New()
			backendAddr, backendSize = writeStr(t, i, 100, "origin")
			if status := test.send(i, int32(requestID), 999, backendAddr, backendSize); status != XqdErrInvalidHandle {
				t.Fatalf("send with missing body status = %d, want %d", status, XqdErrInvalidHandle)
			}
			if i.requests.Get(requestID) != nil {
				t.Fatal("request handle was not consumed before the missing-body error")
			}
		})
	}
}

func TestAsyncRequestV2OnlyOneEnablesStreaming(t *testing.T) {
	i := newOwnershipTestInstance()
	addNoContentBackend(i)
	reqHandle, _ := i.requests.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	backendAddr, backendSize := writeStr(t, i, 100, "origin")

	if status := i.xqd_req_send_async_v2(int32(reqHandle), int32(bodyHandle), backendAddr, backendSize, 2, 200); status != XqdStatusOK {
		t.Fatalf("req_send_async_v2 status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_known_length(int32(bodyHandle), 300); status != XqdErrInvalidHandle {
		t.Fatalf("body handle after non-one streaming flag status = %d, want %d", status, XqdErrInvalidHandle)
	}
	pending := i.pendingRequests.Get(int(i.memory.Uint32(200)))
	select {
	case <-pending.done:
	case <-time.After(time.Second):
		t.Fatal("async request did not complete")
	}
}

func TestResponseSendDownstreamConsumesHandlesByMode(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "streaming"}[streaming], func(t *testing.T) {
			i := newOwnershipTestInstance()
			i.ds_response = httptest.NewRecorder()
			respHandle, _ := i.responses.New()
			bodyHandle, _ := i.bodies.NewBuffer()

			stream := int32(0)
			if streaming {
				stream = 1
			}
			if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), stream); status != XqdStatusOK {
				t.Fatalf("resp_send_downstream status = %d, want %d", status, XqdStatusOK)
			}
			if status := i.xqd_resp_status_get(int32(respHandle), 100); status != XqdErrInvalidHandle {
				t.Fatalf("response handle after send status = %d, want %d", status, XqdErrInvalidHandle)
			}

			bodyStatus := i.xqd_body_known_length(int32(bodyHandle), 104)
			if streaming {
				if bodyStatus != XqdErrNone {
					t.Fatalf("streaming body status = %d, want %d", bodyStatus, XqdErrNone)
				}
				if status := i.xqd_body_close(int32(bodyHandle)); status != XqdStatusOK {
					t.Fatalf("streaming body_close status = %d, want %d", status, XqdStatusOK)
				}
			} else if bodyStatus != XqdErrInvalidHandle {
				t.Fatalf("buffered body handle after send status = %d, want %d", bodyStatus, XqdErrInvalidHandle)
			}
		})
	}
}

func TestResponseSendDownstreamOnlyOneEnablesStreaming(t *testing.T) {
	i := newOwnershipTestInstance()
	recorder := httptest.NewRecorder()
	i.ds_response = recorder
	respHandle, _ := i.responses.New()
	bodyHandle, body := i.bodies.NewBuffer()
	if _, err := body.Write([]byte("buffered")); err != nil {
		t.Fatal(err)
	}

	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 2); status != XqdStatusOK {
		t.Fatalf("resp_send_downstream status = %d, want %d", status, XqdStatusOK)
	}
	if got := recorder.Body.String(); got != "buffered" {
		t.Fatalf("downstream body = %q, want buffered send body %q", got, "buffered")
	}
	if status := i.xqd_body_known_length(int32(bodyHandle), 100); status != XqdErrInvalidHandle {
		t.Fatalf("body handle after non-one streaming flag status = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestResponseSendDownstreamRejectsAlreadyStreamingBody(t *testing.T) {
	i := newOwnershipTestInstance()
	i.ds_response = httptest.NewRecorder()
	responseID, _ := i.responses.New()
	bodyID, body := i.bodies.NewBuffer()
	body.isStreaming = true
	body.streamingChan = make(chan []byte, 1)
	body.streamingDone = make(chan struct{})
	body.streamingAbandon = make(chan struct{})

	if status := i.xqd_resp_send_downstream(int32(responseID), int32(bodyID), 0); status != XqdErrInvalidHandle {
		t.Fatalf("resp_send_downstream status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if i.responses.Get(responseID) != nil {
		t.Fatal("response handle was not consumed before the body error")
	}
	if got := i.bodies.Get(bodyID); got != body {
		t.Fatal("rejected streaming body handle was consumed")
	}
	if status := i.xqd_body_abandon(int32(bodyID)); status != XqdStatusOK {
		t.Fatalf("body_abandon status = %d, want %d", status, XqdStatusOK)
	}

	responseID, _ = i.responses.New()
	if status := i.xqd_resp_send_downstream(int32(responseID), 999, 0); status != XqdErrInvalidHandle {
		t.Fatalf("resp_send_downstream with missing body status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if i.responses.Get(responseID) != nil {
		t.Fatal("response handle was not consumed before the missing-body error")
	}
}

func TestResponseSendDownstreamRejectsDisallowedInformationalStatus(t *testing.T) {
	i := newOwnershipTestInstance()
	i.ds_response = httptest.NewRecorder()
	respHandle, resp := i.responses.New()
	resp.StatusCode = http.StatusSwitchingProtocols
	bodyHandle, _ := i.bodies.NewBuffer()

	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 0); status != XqdErrInvalidArgument {
		t.Fatalf("resp_send_downstream status = %d, want %d", status, XqdErrInvalidArgument)
	}
	if status := i.xqd_resp_status_get(int32(respHandle), 100); status != XqdErrInvalidHandle {
		t.Fatalf("response handle after rejected send status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_known_length(int32(bodyHandle), 104); status != XqdStatusOK {
		t.Fatalf("body handle after rejected send status = %d, want still-valid body", status)
	}
}

func TestStreamingDownstreamBodyRejectsFrontWrites(t *testing.T) {
	i := newOwnershipTestInstance()
	i.ds_response = httptest.NewRecorder()
	respHandle, _ := i.responses.New()
	bodyHandle, _ := i.bodies.NewBuffer()
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(bodyHandle), 1); status != XqdStatusOK {
		t.Fatalf("resp_send_downstream status = %d, want %d", status, XqdStatusOK)
	}
	_, _ = i.memory.WriteAt([]byte("front"), 100)
	if status := i.xqd_body_write(int32(bodyHandle), 100, 5, BodyWriteEndFront, 200); status != XqdErrUnsupported {
		t.Fatalf("front body_write status = %d, want %d", status, XqdErrUnsupported)
	}
	if status := i.xqd_body_close(int32(bodyHandle)); status != XqdStatusOK {
		t.Fatalf("body_close status = %d, want %d", status, XqdStatusOK)
	}
}

func TestRequestAndResponseCloseConsumeHandles(t *testing.T) {
	i := newOwnershipTestInstance()
	reqHandle, _ := i.requests.New()
	respHandle, _ := i.responses.New()

	if status := i.xqd_req_close(int32(reqHandle)); status != XqdStatusOK {
		t.Fatalf("req_close status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_req_version_get(int32(reqHandle), 100); status != XqdErrInvalidHandle {
		t.Fatalf("request handle after close status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_req_close(int32(reqHandle)); status != XqdErrInvalidHandle {
		t.Fatalf("second req_close status = %d, want %d", status, XqdErrInvalidHandle)
	}

	if status := i.xqd_resp_close(int32(respHandle)); status != XqdStatusOK {
		t.Fatalf("resp_close status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_resp_status_get(int32(respHandle), 104); status != XqdErrInvalidHandle {
		t.Fatalf("response handle after close status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_resp_close(int32(respHandle)); status != XqdErrInvalidHandle {
		t.Fatalf("second resp_close status = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestResetSkipsConsumedHandleSlots(t *testing.T) {
	i := newOwnershipTestInstance()
	i.kvStores = &KVStoreHandles{}
	i.kvLookups = &KVStoreLookupHandles{}
	i.kvInserts = &KVStoreInsertHandles{}
	i.kvDeletes = &KVStoreDeleteHandles{}
	i.kvLists = &KVStoreListHandles{}
	i.secretStoreHandles = &SecretStoreHandles{}
	i.secretHandles = &SecretHandles{}
	i.cacheHandles = &CacheHandles{}
	i.cacheBusyHandles = &CacheBusyHandles{}
	i.cacheReplaceHandles = &CacheReplaceHandles{}
	i.aclHandles = &AclHandles{}
	i.asyncItems = &AsyncItemHandles{}

	requestID, _ := i.requests.New()
	responseID, _ := i.responses.New()
	bodyID, _ := i.bodies.NewBuffer()
	_, promise := i.requestPromises.New()
	if i.requests.Take(requestID) == nil || i.responses.Take(responseID) == nil || i.bodies.Take(bodyID) == nil {
		t.Fatal("failed to create consumed handle slots")
	}

	i.reset()
	if !promise.IsReady() {
		t.Fatal("reset did not cancel an outstanding downstream request promise")
	}
}

func TestKVWaitsConsumePendingHandles(t *testing.T) {
	t.Run("lookup_v1", func(t *testing.T) {
		i := newOwnershipTestInstance()
		i.kvLookups = &KVStoreLookupHandles{}
		handleID, handle := i.kvLookups.New()
		handle.Complete(nil, nil)

		if status := i.xqd_kv_store_lookup_wait(int32(handleID), 100, 104, 0, 108, 112, 116); status != XqdStatusOK {
			t.Fatalf("first lookup_wait status = %d, want %d", status, XqdStatusOK)
		}
		if status := i.xqd_kv_store_lookup_wait(int32(handleID), 100, 104, 0, 108, 112, 116); status != XqdErrInvalidHandle {
			t.Fatalf("second lookup_wait status = %d, want %d", status, XqdErrInvalidHandle)
		}
	})

	t.Run("lookup_v2", func(t *testing.T) {
		i := newOwnershipTestInstance()
		i.kvLookups = &KVStoreLookupHandles{}
		handleID, handle := i.kvLookups.New()
		handle.Complete(nil, nil)

		if status := i.xqd_kv_store_lookup_wait_v2(int32(handleID), 100, 104, 0, 108, 112, 120); status != XqdStatusOK {
			t.Fatalf("first lookup_wait_v2 status = %d, want %d", status, XqdStatusOK)
		}
		if status := i.xqd_kv_store_lookup_wait_v2(int32(handleID), 100, 104, 0, 108, 112, 120); status != XqdErrInvalidHandle {
			t.Fatalf("second lookup_wait_v2 status = %d, want %d", status, XqdErrInvalidHandle)
		}
	})

	t.Run("insert", func(t *testing.T) {
		i := newOwnershipTestInstance()
		i.kvInserts = &KVStoreInsertHandles{}
		handleID, handle := i.kvInserts.New()
		handle.Complete(1, nil)

		if status := i.xqd_kv_store_insert_wait(int32(handleID), 100); status != XqdStatusOK {
			t.Fatalf("first insert_wait status = %d, want %d", status, XqdStatusOK)
		}
		if status := i.xqd_kv_store_insert_wait(int32(handleID), 100); status != XqdErrInvalidHandle {
			t.Fatalf("second insert_wait status = %d, want %d", status, XqdErrInvalidHandle)
		}
	})

	t.Run("delete", func(t *testing.T) {
		i := newOwnershipTestInstance()
		i.kvDeletes = &KVStoreDeleteHandles{}
		handleID, handle := i.kvDeletes.New()
		handle.Complete(nil)

		if status := i.xqd_kv_store_delete_wait(int32(handleID), 100); status != XqdStatusOK {
			t.Fatalf("first delete_wait status = %d, want %d", status, XqdStatusOK)
		}
		if status := i.xqd_kv_store_delete_wait(int32(handleID), 100); status != XqdErrInvalidHandle {
			t.Fatalf("second delete_wait status = %d, want %d", status, XqdErrInvalidHandle)
		}
	})

	t.Run("list", func(t *testing.T) {
		i := newOwnershipTestInstance()
		i.kvLists = &KVStoreListHandles{}
		handleID, handle := i.kvLists.New()
		handle.Complete(&ListResult{}, nil)

		if status := i.xqd_kv_store_list_wait(int32(handleID), 100, 104); status != XqdStatusOK {
			t.Fatalf("first list_wait status = %d, want %d", status, XqdStatusOK)
		}
		if status := i.xqd_kv_store_list_wait(int32(handleID), 100, 104); status != XqdErrInvalidHandle {
			t.Fatalf("second list_wait status = %d, want %d", status, XqdErrInvalidHandle)
		}
	})
}

func TestCacheWaitAndCloseConsumeHandles(t *testing.T) {
	i := newOwnershipTestInstance()
	i.cacheHandles = &CacheHandles{}
	i.cacheBusyHandles = &CacheBusyHandles{}

	newReadyTransaction := func() *CacheTransaction {
		tx := &CacheTransaction{ready: make(chan struct{})}
		close(tx.ready)
		return tx
	}

	busyID := i.cacheBusyHandles.New(newReadyTransaction())
	if status := i.xqd_cache_busy_handle_wait(int32(busyID), 100); status != XqdStatusOK {
		t.Fatalf("first cache_busy_handle_wait status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_cache_busy_handle_wait(int32(busyID), 100); status != XqdErrInvalidHandle {
		t.Fatalf("second cache_busy_handle_wait status = %d, want %d", status, XqdErrInvalidHandle)
	}

	cacheID := int(i.memory.Uint32(100))
	if status := i.xqd_cache_close(int32(cacheID)); status != XqdStatusOK {
		t.Fatalf("first cache_close status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_cache_close(int32(cacheID)); status != XqdErrInvalidHandle {
		t.Fatalf("second cache_close status = %d, want %d", status, XqdErrInvalidHandle)
	}

	busyID = i.cacheBusyHandles.New(newReadyTransaction())
	if status := i.xqd_cache_close_busy(int32(busyID)); status != XqdStatusOK {
		t.Fatalf("first cache_close_busy status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_cache_close_busy(int32(busyID)); status != XqdErrInvalidHandle {
		t.Fatalf("second cache_close_busy status = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestBodyAppendWritesIntoStreamingRequest(t *testing.T) {
	i := newOwnershipTestInstance()
	gotBody := make(chan string, 1)
	i.addBackend("origin", &Backend{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody <- string(body)
		w.WriteHeader(http.StatusNoContent)
	})})
	reqHandle, _ := i.requests.New()
	destHandle, _ := i.bodies.NewBuffer()
	srcHandle, src := i.bodies.NewBuffer()
	if _, err := src.Write([]byte("appended")); err != nil {
		t.Fatal(err)
	}
	backendAddr, backendSize := writeStr(t, i, 100, "origin")
	if status := i.xqd_req_send_async_streaming(int32(reqHandle), int32(destHandle), backendAddr, backendSize, 200); status != XqdStatusOK {
		t.Fatalf("req_send_async_streaming status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_append(int32(destHandle), int32(srcHandle)); status != XqdStatusOK {
		t.Fatalf("body_append status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_known_length(int32(srcHandle), 300); status != XqdErrInvalidHandle {
		t.Fatalf("source body after append status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if status := i.xqd_body_close(int32(destHandle)); status != XqdStatusOK {
		t.Fatalf("body_close status = %d, want %d", status, XqdStatusOK)
	}
	select {
	case got := <-gotBody:
		if got != "appended" {
			t.Fatalf("streaming request body = %q, want %q", got, "appended")
		}
	case <-time.After(time.Second):
		t.Fatal("streaming request was not dispatched")
	}
}

func TestBodyAppendWritesIntoStreamingDownstreamResponse(t *testing.T) {
	i := newOwnershipTestInstance()
	recorder := httptest.NewRecorder()
	i.ds_response = recorder
	respHandle, _ := i.responses.New()
	destHandle, _ := i.bodies.NewBuffer()
	srcHandle, src := i.bodies.NewBuffer()
	if _, err := src.Write([]byte("appended")); err != nil {
		t.Fatal(err)
	}
	if status := i.xqd_resp_send_downstream(int32(respHandle), int32(destHandle), 1); status != XqdStatusOK {
		t.Fatalf("resp_send_downstream status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_append(int32(destHandle), int32(srcHandle)); status != XqdStatusOK {
		t.Fatalf("body_append status = %d, want %d", status, XqdStatusOK)
	}
	if status := i.xqd_body_close(int32(destHandle)); status != XqdStatusOK {
		t.Fatalf("body_close status = %d, want %d", status, XqdStatusOK)
	}
	if got := recorder.Body.String(); got != "appended" {
		t.Fatalf("streaming downstream body = %q, want %q", got, "appended")
	}
}

func TestBodyAppendRejectsStreamingSourceWithoutConsumingIt(t *testing.T) {
	i := newOwnershipTestInstance()
	destHandle, _ := i.bodies.NewBuffer()
	srcHandle, src := i.bodies.NewBuffer()
	src.isStreaming = true
	src.streamingChan = make(chan []byte, 1)
	src.streamingDone = make(chan struct{})
	src.streamingAbandon = make(chan struct{})

	if status := i.xqd_body_append(int32(destHandle), int32(srcHandle)); status != XqdErrInvalidHandle {
		t.Fatalf("body_append status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if got := i.bodies.Get(srcHandle); got != src {
		t.Fatal("rejected streaming source was consumed")
	}
	if status := i.xqd_body_abandon(int32(srcHandle)); status != XqdStatusOK {
		t.Fatalf("body_abandon status = %d, want %d", status, XqdStatusOK)
	}
}

func TestBodyAppendToSelfConsumesNonStreamingSource(t *testing.T) {
	i := newOwnershipTestInstance()
	handle, _ := i.bodies.NewBuffer()

	if status := i.xqd_body_append(int32(handle), int32(handle)); status != XqdErrInvalidHandle {
		t.Fatalf("body_append status = %d, want %d", status, XqdErrInvalidHandle)
	}
	if i.bodies.Get(handle) != nil {
		t.Fatal("self-appended non-streaming source was not consumed")
	}
}

func TestDownstreamRequestPromiseWaitAndAbandonConsumeHandles(t *testing.T) {
	i := newOwnershipTestInstance()
	waitHandle, waitPromise := i.requestPromises.New()
	waitPromise.Complete(nil, errors.New("no request"))
	if status := i.xqd_http_downstream_next_request_wait(int32(waitHandle), 100, 104); status != XqdErrNone {
		t.Fatalf("next_request_wait status = %d, want %d", status, XqdErrNone)
	}
	if status := i.xqd_http_downstream_next_request_wait(int32(waitHandle), 100, 104); status != XqdErrInvalidHandle {
		t.Fatalf("second next_request_wait status = %d, want %d", status, XqdErrInvalidHandle)
	}

	abandonHandle, abandonPromise := i.requestPromises.New()
	if status := i.xqd_http_downstream_next_request_abandon(int32(abandonHandle)); status != XqdStatusOK {
		t.Fatalf("next_request_abandon status = %d, want %d", status, XqdStatusOK)
	}
	if !abandonPromise.IsReady() {
		t.Fatal("abandoned request promise was not completed")
	}
	if status := i.xqd_http_downstream_next_request_abandon(int32(abandonHandle)); status != XqdErrInvalidHandle {
		t.Fatalf("second next_request_abandon status = %d, want %d", status, XqdErrInvalidHandle)
	}
}

func TestDownstreamNextRequestUsesU64TimeoutAndCorrectMaskBit(t *testing.T) {
	t.Run("explicit_timeout", func(t *testing.T) {
		i := newOwnershipTestInstance()
		i.memory.PutUint64(20, 100)
		if status := i.xqd_http_downstream_next_request(nextRequestOptionsMaskTimeout, 100, 200); status != XqdStatusOK {
			t.Fatalf("next_request status = %d, want %d", status, XqdStatusOK)
		}
		handle := int32(i.memory.Uint32(200))
		started := time.Now()
		if status := i.xqd_http_downstream_next_request_wait(handle, 300, 304); status != XqdErrNone {
			t.Fatalf("next_request_wait status = %d, want %d", status, XqdErrNone)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("explicit 20ms timeout took %v", elapsed)
		}
	})

	t.Run("full_u64_field", func(t *testing.T) {
		i := newOwnershipTestInstance()
		i.memory.PutUint64(1<<32|1, 100)
		if status := i.xqd_http_downstream_next_request(nextRequestOptionsMaskTimeout, 100, 200); status != XqdStatusOK {
			t.Fatalf("next_request status = %d, want %d", status, XqdStatusOK)
		}
		handle := int(i.memory.Uint32(200))
		promise := i.requestPromises.Get(handle)
		time.Sleep(10 * time.Millisecond)
		if promise.IsReady() {
			t.Fatal("u64 timeout was truncated to its low 32 bits")
		}
		if status := i.xqd_http_downstream_next_request_abandon(int32(handle)); status != XqdStatusOK {
			t.Fatalf("next_request_abandon status = %d, want %d", status, XqdStatusOK)
		}
	})

	t.Run("reserved_bit_is_not_timeout", func(t *testing.T) {
		i := newOwnershipTestInstance()
		i.memory.PutUint64(0, 100)
		if status := i.xqd_http_downstream_next_request(1, 100, 200); status != XqdStatusOK {
			t.Fatalf("next_request status = %d, want %d", status, XqdStatusOK)
		}
		handle := int(i.memory.Uint32(200))
		promise := i.requestPromises.Get(handle)
		time.Sleep(10 * time.Millisecond)
		if promise.IsReady() {
			t.Fatal("reserved options bit was interpreted as the timeout bit")
		}
		if status := i.xqd_http_downstream_next_request_abandon(int32(handle)); status != XqdStatusOK {
			t.Fatalf("next_request_abandon status = %d, want %d", status, XqdStatusOK)
		}
	})
}

func TestDownstreamNextRequestRejectsInvalidMemoryBeforeCreatingPromise(t *testing.T) {
	tests := []struct {
		name       string
		optionsPtr int32
		handleOut  int32
	}{
		{name: "options", optionsPtr: 2044, handleOut: 100},
		{name: "handle_out", optionsPtr: 100, handleOut: 2046},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := newOwnershipTestInstance()
			if status := i.xqd_http_downstream_next_request(0, tt.optionsPtr, tt.handleOut); status != XqdError {
				t.Fatalf("next_request status = %d, want %d", status, XqdError)
			}
			if got := len(i.requestPromises.handles); got != 0 {
				t.Fatalf("created %d request promises after invalid guest memory, want 0", got)
			}
		})
	}
}

func TestPendingRequestPollV2InitializesErrorDetailWhilePending(t *testing.T) {
	i := newOwnershipTestInstance()
	handleID, _ := i.pendingRequests.New()
	i.memory.PutUint32(0xFFFFFFFF, 100)

	if status := i.xqd_pending_req_poll_v2(int32(handleID), 100, 120, 124, 128); status != XqdStatusOK {
		t.Fatalf("pending_req_poll_v2 status = %d, want %d", status, XqdStatusOK)
	}
	if got := i.memory.Uint32(100); got != SendErrorDetailOk {
		t.Fatalf("error detail tag = %d, want Ok tag %d", got, SendErrorDetailOk)
	}
	if got := i.memory.Uint32(120); got != 0 {
		t.Fatalf("is_done = %d, want 0", got)
	}
}
