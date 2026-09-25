package fastlike

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
)

// xqd_init initializes the XQD ABI and verifies the protocol version.
// The XQD ABI currently only supports version 1.
// Returns XqdErrUnsupported if the version is not supported, XqdStatusOK otherwise.
func (i *Instance) xqd_init(abiv int64) int32 {
	i.abilog.Printf("init: version=%d\n", abiv)
	const supportedABIVersion = 1
	if abiv != supportedABIVersion {
		return XqdErrUnsupported
	}

	return XqdStatusOK
}

// xqd_req_body_downstream_get converts the downstream HTTP request into a (request, body) handle pair.
// Captures TLS state, original header names, and sets up the request URL with scheme and host.
// This is typically the first XQD call made by guest programs to access the incoming HTTP request.
// Returns XqdStatusOK on success.
func (i *Instance) xqd_req_body_downstream_get(request_handle_out int32, body_handle_out int32) int32 {
	// Create a new request handle and clone the downstream request into it
	rhid, rh := i.requests.New()
	rh.Request = i.ds_request.Clone(context.Background())
	switch {
	case i.ds_request.ProtoMajor == 3:
		rh.version = Http3
	case i.ds_request.ProtoMajor == 2:
		rh.version = Http2
	case i.ds_request.ProtoMajor == 1 && i.ds_request.ProtoMinor == 0:
		rh.version = Http10
	case i.ds_request.ProtoMajor == 0 && i.ds_request.ProtoMinor == 9:
		rh.version = Http09
	default:
		rh.version = Http11
	}

	// The downstream request URL doesn't include host or scheme, so we populate them
	rh.URL.Host = i.ds_request.Host

	if i.secureFn(i.ds_request) {
		rh.URL.Scheme = "https"
		rh.Header.Set("fastly-ssl", "1")
	} else {
		rh.URL.Scheme = "http"
	}

	// Header names as the client sent them, for downstream_original_header_names.
	rh.originalHeaders = i.ds_originalHeaders
	if rh.originalHeaders == nil {
		rh.originalHeaders = originalHeaderNamesFromRequest(i.ds_request)
	}

	// Capture TLS connection state if the request was over TLS
	if i.ds_request.TLS != nil {
		rh.tlsState = i.ds_request.TLS
	}

	// Create a body handle by copying the downstream request body into a buffer.
	// NOTE: We use NewBuffer instead of NewReader to avoid a bug where subrequests
	// don't properly forward the body. This copies the entire body into memory,
	// which works around the issue but may not be ideal for very large bodies.
	bhid, bh := i.bodies.NewBuffer()
	if i.ds_request.Body != nil {
		_, _ = io.Copy(bh, i.ds_request.Body)
		_ = i.ds_request.Body.Close()
	}
	bh.trailers = i.ds_request.Trailer.Clone()

	i.memory.PutUint32(uint32(rhid), int64(request_handle_out))
	i.memory.PutUint32(uint32(bhid), int64(body_handle_out))

	// Store the downstream request handle for implicit downstream request operations
	i.downstreamRequestHandle = int32(rhid)

	i.abilog.Printf("req_body_downstream_get: rh=%d bh=%d", rhid, bhid)

	return XqdStatusOK
}

// xqd_resp_send_downstream returns before the body is sent, like production.
// With stream set to 1, the guest streams the rest through the body handle.
// Only one final response can go out, after any number of 103s.
func (i *Instance) xqd_resp_send_downstream(whandle int32, bhandle int32, stream int32) int32 {
	if i.ds_done != nil {
		i.abilog.Printf("resp_send_downstream: a response was already sent")
		return XqdError
	}
	w := i.responses.Get(int(whandle))
	if w == nil {
		i.abilog.Printf("resp_send_downstream: invalid response handle %d", whandle)
		return XqdErrInvalidHandle
	}
	w = i.responses.Take(int(whandle))
	if w.StatusCode >= 100 && w.StatusCode < 200 && w.StatusCode != http.StatusEarlyHints {
		return XqdErrInvalidArgument
	}
	b := i.bodies.Get(int(bhandle))
	if b == nil {
		i.abilog.Printf("resp_send_downstream: invalid body handle %d", bhandle)
		return XqdErrInvalidHandle
	}
	if b.IsStreaming() {
		return XqdErrInvalidHandle
	}

	headers := w.Header.Clone()
	effectiveMode := validateAndApplyFramingMode(headers, w.framingHeadersMode, func(format string, args ...interface{}) {
		i.abilog.Printf("resp_send_downstream: "+format, args...)
	})
	i.abilog.Printf("resp_send_downstream: stream=%d framing_mode=%d effective_mode=%d", stream, w.framingHeadersMode, effectiveMode)

	if w.StatusCode == http.StatusEarlyHints {
		// Production drops the body, but keeps a streaming handle open.
		_ = b.Close()
		if stream == 1 {
			b.becomeSink(&downstreamStream{stopped: true})
		} else {
			i.bodies.Take(int(bhandle))
		}
		i.sendEarlyHints(headers)
		return XqdStatusOK
	}

	status := w.StatusCode
	hasBody := i.downstreamHasBody(status)
	if stream != 1 {
		b = i.bodies.Take(int(bhandle))
		i.sendDownstream(func(rw http.ResponseWriter) error {
			defer func() { _ = b.Close() }()
			return writeWholeResponse(rw, status, headers, hasBody, b, b.currentTrailers)
		})
		return XqdStatusOK
	}

	// What the body held goes out first.
	sink := newDownstreamStream(&BodyHandle{reader: b.reader, closer: b.closer}, b)
	b.becomeSink(sink)
	i.sendDownstream(func(rw http.ResponseWriter) error {
		return sink.send(rw, status, headers, hasBody)
	})
	return XqdStatusOK
}

// xqd_resp_send_downstream_pending returns at once, like production, and
// traps if a final response went out already.
func (i *Instance) xqd_resp_send_downstream_pending(phandle int32) int32 {
	pr := i.pendingRequests.Take(int(phandle))
	if pr == nil {
		i.abilog.Printf("send_downstream_pending: invalid pending handle=%d", phandle)
		return XqdErrInvalidHandle
	}
	if i.ds_done != nil {
		if pr.cancel != nil {
			pr.cancel()
		}
		panic(guestTrap("send_downstream_pending: a response was already sent"))
	}
	if i.trace != nil {
		pr.observeWait(i.trace.WallStart)
	}

	// The response body (real or synthetic) is consumed here, so claim the
	// close so the recorder's late-completion hook skips this request.
	pr.bodyClosed.Store(true)

	i.sendDownstream(func(w http.ResponseWriter) error {
		resp, err := pr.Wait()
		queued := &pr.headersResp
		if err != nil {
			i.abilog.Printf("send_downstream_pending: request failed: %s", err.Error())
			resp = sendFailureResponse(err)
			queued = &pr.headersErr
		}
		defer func() { _ = resp.Body.Close() }()

		headers := resp.Header.Clone()
		if headers == nil {
			headers = http.Header{}
		}
		queued.Apply(headers)
		effectiveMode := validateAndApplyFramingMode(headers, FramingHeadersModeAutomatic, func(format string, args ...interface{}) {
			i.abilog.Printf("send_downstream_pending: "+format, args...)
		})
		i.abilog.Printf("send_downstream_pending: status=%d effective_mode=%d", resp.StatusCode, effectiveMode)

		return writeWholeResponse(w, resp.StatusCode, headers, i.downstreamHasBody(resp.StatusCode), resp.Body, responseTrailers(resp))
	})
	return XqdStatusOK
}

// xqd_req_downstream_client_ip_addr extracts the client IP address from the downstream request.
// Parses the RemoteAddr field and writes the IP octets to guest memory.
// IPv4 addresses are returned in 4-byte format, IPv6 in 16-byte format.
// Returns XqdStatusOK on success or XqdError on failure.
func (i *Instance) xqd_req_downstream_client_ip_addr(octets_out int32, nwritten_out int32) int32 {
	// Extract IP from RemoteAddr (format is typically "IP:port")
	hostPort := strings.SplitN(i.ds_request.RemoteAddr, ":", 2)
	ip := net.ParseIP(hostPort[0])
	i.abilog.Printf("req_downstream_client_ip_addr: remoteaddr=%s, ip=%q\n", i.ds_request.RemoteAddr, ip)

	// If we can't parse the IP, return success with zero bytes written
	if ip == nil {
		return XqdStatusOK
	}

	// Convert IPv6-mapped IPv4 addresses (::ffff:x.x.x.x) to native IPv4 (4 bytes)
	// This ensures IPv4 addresses are always returned in 4-byte format
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
	}

	// Write the IP bytes to guest memory. net.IP is a byte slice that can be written directly.
	nwritten, err := i.memory.WriteAt(ip, int64(octets_out))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

// xqd_req_downstream_server_ip_addr returns the server's IP address that received the downstream request.
// For local testing, this returns 127.0.0.1. In a production Fastly environment, this would be
// the actual server IP that accepted the connection.
// Returns XqdStatusOK on success or XqdError on failure.
func (i *Instance) xqd_req_downstream_server_ip_addr(octets_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("req_downstream_server_ip_addr")

	// Return localhost (127.0.0.1) for local testing
	// Use a byte slice to ensure proper 4-byte IPv4 format
	ip := []byte{127, 0, 0, 1}

	// Write the IP to memory
	nwritten, err := i.memory.WriteAt(ip, int64(octets_out))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

// xqd_uap_parse parses a user agent string into its component parts.
// Extracts family, major, minor, and patch version information and writes them to guest memory.
// Returns XqdStatusOK on success or XqdError if memory operations fail.
func (i *Instance) xqd_uap_parse(
	addr int32, size int32,
	family_out, family_maxlen, family_nwritten_out int32,
	major_out, major_maxlen, major_nwritten_out int32,
	minor_out, minor_maxlen, minor_nwritten_out int32,
	patch_out, patch_maxlen, patch_nwritten_out int32,
) int32 {
	buf := make([]byte, size)
	_, err := i.memory.ReadAt(buf, int64(addr))
	if err != nil {
		i.abilog.Printf("uap_parse: read err, got %s", err.Error())
		return XqdError
	}

	useragent := string(buf)
	i.abilog.Printf("uap_parse: useragent=%s\n", useragent)

	ua := i.uaparser(useragent)

	family_nwritten, err := i.memory.WriteAt([]byte(ua.Family), int64(family_out))
	if err != nil {
		i.abilog.Printf("uap_parse: family write err, got %s", err.Error())
		return XqdError
	}
	i.memory.PutUint32(uint32(family_nwritten), int64(family_nwritten_out))

	major_nwritten, err := i.memory.WriteAt([]byte(ua.Major), int64(major_out))
	if err != nil {
		i.abilog.Printf("uap_parse: major write err, got %s", err.Error())
		return XqdError
	}
	i.memory.PutUint32(uint32(major_nwritten), int64(major_nwritten_out))

	minor_nwritten, err := i.memory.WriteAt([]byte(ua.Minor), int64(minor_out))
	if err != nil {
		i.abilog.Printf("uap_parse: minor write err, got %s", err.Error())
		return XqdError
	}
	i.memory.PutUint32(uint32(minor_nwritten), int64(minor_nwritten_out))

	patch_nwritten, err := i.memory.WriteAt([]byte(ua.Patch), int64(patch_out))
	if err != nil {
		i.abilog.Printf("uap_parse: patch write err, got %s", err.Error())
		return XqdError
	}
	i.memory.PutUint32(uint32(patch_nwritten), int64(patch_nwritten_out))

	return XqdStatusOK
}

// logStubCall logs stub function calls with their arguments for debugging purposes.
// Used to track unimplemented or stubbed XQD ABI functions during development.
func logStubCall(logger *log.Logger, functionName string, args ...int32) {
	argStrings := []string{}
	for _, arg := range args {
		argStrings = append(argStrings, fmt.Sprintf("%d", arg))
	}

	logger.Printf("[STUB] %s: args=%q\n", functionName, argStrings)
}

// wasm1 creates a stub function that accepts one int32 argument.
// Returns a function that logs the call and returns XqdErrUnsupported (5).
// Used during development to identify unimplemented XQD functions.
func (i *Instance) wasm1(name string) func(a int32) int32 {
	return func(a int32) int32 {
		logStubCall(i.abilog, name, a)
		return XqdErrUnsupported
	}
}

// wasm6 creates a stub function that accepts six int32 arguments.
// Returns a function that logs the call and returns XqdErrUnsupported (5).
// Used during development to identify unimplemented XQD functions.
func (i *Instance) wasm6(name string) func(a, b, c, d, e, f int32) int32 {
	return func(a, b, c, d, e, f int32) int32 {
		logStubCall(i.abilog, name, a, b, c, d, e, f)
		return XqdErrUnsupported
	}
}
