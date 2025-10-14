package fastlike

import (
	"bytes"
	"io"
	"net/http"
)

// RequestHandle is an http.Request with extra metadata
// Notably, the request body is ignored and instead the guest will provide a BodyHandle to use
type RequestHandle struct {
	*http.Request
	// autoDecompressEncodings is a bitfield indicating which encodings to auto-decompress
	autoDecompressEncodings uint32
}

// RequestHandles is a slice of RequestHandle with functions to get and create
type RequestHandles struct {
	handles []*RequestHandle
}

// Get returns the RequestHandle identified by id or nil if one does not exist.
func (rhs *RequestHandles) Get(id int) *RequestHandle {
	if id < 0 || id >= len(rhs.handles) {
		return nil
	}

	return rhs.handles[id]
}

// New creates a new RequestHandle and returns its handle id and the handle itself.
func (rhs *RequestHandles) New() (int, *RequestHandle) {
	rh := &RequestHandle{Request: &http.Request{}}
	rhs.handles = append(rhs.handles, rh)
	return len(rhs.handles) - 1, rh
}

// ResponseHandle is an http.Response with extra metadata
// Notably, the response body is ignored and instead the guest will provide a BodyHandle to use
type ResponseHandle struct {
	*http.Response
	// RemoteAddr is the remote address of the backend that handled the request
	RemoteAddr string
}

// ResponseHandles is a slice of ResponseHandle with functions to get and create
type ResponseHandles struct {
	handles []*ResponseHandle
}

// Get returns the ResponseHandle identified by id or nil if one does not exist.
func (rhs *ResponseHandles) Get(id int) *ResponseHandle {
	if id < 0 || id >= len(rhs.handles) {
		return nil
	}

	return rhs.handles[id]
}

// New creates a new ResponseHandle and returns its handle id and the handle itself.
func (rhs *ResponseHandles) New() (int, *ResponseHandle) {
	rh := &ResponseHandle{Response: &http.Response{StatusCode: 200}}
	rhs.handles = append(rhs.handles, rh)
	return len(rhs.handles) - 1, rh
}

// BodyHandle represents a body. It could be readable or writable, but not both.
// For cases where it's already connected to a request or response body, the reader or writer
// properties will reference the original request or response respectively.
// For new bodies, buf will hold the contents and either the reader or writer will wrap it.
type BodyHandle struct {
	// reader, writer, and closer are connected to the existing request/response body, if one exists
	reader io.Reader
	writer io.Writer
	closer io.Closer

	// for bodies created outside of a request/response, buf holds the body content and
	// reader/writer/closer wrap it
	buf *bytes.Buffer

	// length is the number of bytes in the body
	length int64

	// trailers are HTTP trailers that come after the body in chunked encoding
	trailers http.Header
}

// Close implements io.Closer for a BodyHandle
func (b *BodyHandle) Close() error {
	if b.closer != nil {
		return b.closer.Close()
	}
	return nil
}

// Read implements io.Reader for a BodyHandle
func (b *BodyHandle) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

// Write implements io.Writer for a BodyHandle
func (b *BodyHandle) Write(p []byte) (int, error) {
	n, e := b.writer.Write(p)
	b.length += int64(n)
	return n, e
}

func (b *BodyHandle) Size() int64 {
	if b.length == 0 {
		return -1
	}
	return b.length
}

// BodyHandles is a slice of BodyHandle with methods to get and create
type BodyHandles struct {
	handles []*BodyHandle
}

// Get returns the BodyHandle identified by id or nil if one does not exist
func (bhs *BodyHandles) Get(id int) *BodyHandle {
	if id < 0 || id >= len(bhs.handles) {
		return nil
	}

	return bhs.handles[id]
}

// NewBuffer creates a BodyHandle backed by a buffer which can be read from or written to
func (bhs *BodyHandles) NewBuffer() (int, *BodyHandle) {
	bh := &BodyHandle{buf: new(bytes.Buffer)}
	bh.reader = io.Reader(bh.buf)
	bh.writer = io.Writer(bh.buf)
	bhs.handles = append(bhs.handles, bh)
	return len(bhs.handles) - 1, bh
}

// NewReader creates a BodyHandle whose reader and closer is connected to the supplied ReadCloser
func (bhs *BodyHandles) NewReader(rdr io.ReadCloser) (int, *BodyHandle) {
	bh := &BodyHandle{}
	bh.reader = rdr
	bh.closer = rdr
	bh.writer = io.Discard
	bhs.handles = append(bhs.handles, bh)
	return len(bhs.handles) - 1, bh
}

// NewWriter creates a BodyHandle whose writer is connected to the supplied Writer
func (bhs *BodyHandles) NewWriter(w io.Writer) (int, *BodyHandle) {
	bh := &BodyHandle{}
	bh.writer = w
	bhs.handles = append(bhs.handles, bh)
	return len(bhs.handles) - 1, bh
}

// PendingRequest represents an asynchronous HTTP request in flight
type PendingRequest struct {
	done     chan struct{}  // closed when request completes
	response *http.Response // response when complete
	err      error          // error if request failed
}

// IsReady checks if the pending request has completed (non-blocking)
func (pr *PendingRequest) IsReady() bool {
	select {
	case <-pr.done:
		return true
	default:
		return false
	}
}

// Wait blocks until the pending request completes and returns the response
func (pr *PendingRequest) Wait() (*http.Response, error) {
	<-pr.done
	return pr.response, pr.err
}

// PendingRequestHandles is a slice of PendingRequest with methods to get and create
type PendingRequestHandles struct {
	handles []*PendingRequest
}

// Get returns the PendingRequest identified by id or nil if one does not exist
func (prhs *PendingRequestHandles) Get(id int) *PendingRequest {
	if id < 0 || id >= len(prhs.handles) {
		return nil
	}

	return prhs.handles[id]
}

// New creates a new PendingRequest and returns its handle id and the handle itself
func (prhs *PendingRequestHandles) New() (int, *PendingRequest) {
	pr := &PendingRequest{done: make(chan struct{})}
	prhs.handles = append(prhs.handles, pr)
	return len(prhs.handles) - 1, pr
}

// Complete marks a pending request as completed with the given response or error
func (pr *PendingRequest) Complete(resp *http.Response, err error) {
	pr.response = resp
	pr.err = err
	close(pr.done)
}

// Secret represents a secret value that can be retrieved from a SecretStore
type Secret struct {
	plaintext []byte
}

// Plaintext returns the plaintext bytes of the secret
func (s *Secret) Plaintext() []byte {
	return s.plaintext
}

// SecretHandles is a slice of Secret with methods to get and create
type SecretHandles struct {
	handles []*Secret
}

// Get returns the Secret identified by id or nil if one does not exist
func (shs *SecretHandles) Get(id int) *Secret {
	if id < 0 || id >= len(shs.handles) {
		return nil
	}
	return shs.handles[id]
}

// New creates a new Secret from plaintext bytes and returns its handle id
func (shs *SecretHandles) New(plaintext []byte) int {
	s := &Secret{plaintext: plaintext}
	shs.handles = append(shs.handles, s)
	return len(shs.handles) - 1
}

// SecretStoreHandle represents a reference to a secret store
type SecretStoreHandle struct {
	name string
}

// SecretStoreHandles is a slice of SecretStoreHandle with methods to get and create
type SecretStoreHandles struct {
	handles []*SecretStoreHandle
}

// Get returns the SecretStoreHandle identified by id or nil if one does not exist
func (sshs *SecretStoreHandles) Get(id int) *SecretStoreHandle {
	if id < 0 || id >= len(sshs.handles) {
		return nil
	}
	return sshs.handles[id]
}

// New creates a new SecretStoreHandle and returns its handle id
func (sshs *SecretStoreHandles) New(name string) int {
	ssh := &SecretStoreHandle{name: name}
	sshs.handles = append(sshs.handles, ssh)
	return len(sshs.handles) - 1
}

// CacheHandle represents a cache lookup result (could be found, not found, or must-insert)
type CacheHandle struct {
	Transaction *CacheTransaction // reference to the transaction
	ReadOffset  int64             // current read offset for streaming
}

// CacheHandles is a slice of CacheHandle with methods to get and create
type CacheHandles struct {
	handles []*CacheHandle
}

// Get returns the CacheHandle identified by id or nil if one does not exist
func (chs *CacheHandles) Get(id int) *CacheHandle {
	if id < 0 || id >= len(chs.handles) {
		return nil
	}
	return chs.handles[id]
}

// New creates a new CacheHandle and returns its handle id
func (chs *CacheHandles) New(tx *CacheTransaction) int {
	ch := &CacheHandle{Transaction: tx}
	chs.handles = append(chs.handles, ch)
	return len(chs.handles) - 1
}

// CacheBusyHandle represents a pending async cache lookup
type CacheBusyHandle struct {
	Transaction *CacheTransaction
}

// CacheBusyHandles is a slice of CacheBusyHandle with methods to get and create
type CacheBusyHandles struct {
	handles []*CacheBusyHandle
}

// Get returns the CacheBusyHandle identified by id or nil if one does not exist
func (cbhs *CacheBusyHandles) Get(id int) *CacheBusyHandle {
	if id < 0 || id >= len(cbhs.handles) {
		return nil
	}
	return cbhs.handles[id]
}

// New creates a new CacheBusyHandle and returns its handle id
func (cbhs *CacheBusyHandles) New(tx *CacheTransaction) int {
	cbh := &CacheBusyHandle{Transaction: tx}
	cbhs.handles = append(cbhs.handles, cbh)
	return len(cbhs.handles) - 1
}

// CacheReplaceHandle represents a cache replace operation
type CacheReplaceHandle struct {
	Entry   *CacheEntry
	Options *CacheReplaceOptions
}

// CacheReplaceHandles is a slice of CacheReplaceHandle with methods to get and create
type CacheReplaceHandles struct {
	handles []*CacheReplaceHandle
}

// Get returns the CacheReplaceHandle identified by id or nil if one does not exist
func (crhs *CacheReplaceHandles) Get(id int) *CacheReplaceHandle {
	if id < 0 || id >= len(crhs.handles) {
		return nil
	}
	return crhs.handles[id]
}

// New creates a new CacheReplaceHandle and returns its handle id
func (crhs *CacheReplaceHandles) New(entry *CacheEntry, options *CacheReplaceOptions) int {
	crh := &CacheReplaceHandle{Entry: entry, Options: options}
	crhs.handles = append(crhs.handles, crh)
	return len(crhs.handles) - 1
}

// AclHandle represents a reference to an ACL (Access Control List)
type AclHandle struct {
	name string
	acl  *Acl
}

// AclHandles is a slice of AclHandle with methods to get and create
type AclHandles struct {
	handles []*AclHandle
}

// Get returns the AclHandle identified by id or nil if one does not exist
func (ahs *AclHandles) Get(id int) *AclHandle {
	if id < 0 || id >= len(ahs.handles) {
		return nil
	}
	return ahs.handles[id]
}

// New creates a new AclHandle and returns its handle id
func (ahs *AclHandles) New(name string, acl *Acl) int {
	ah := &AclHandle{name: name, acl: acl}
	ahs.handles = append(ahs.handles, ah)
	return len(ahs.handles) - 1
}
