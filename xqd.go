package fastlike

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strings"
)

func (i *Instance) xqd_init(abiv int64) int32 {
	i.abilog.Printf("init: version=%d\n", abiv)
	if abiv != 1 {
		return XqdErrUnsupported
	}

	return XqdStatusOK
}

func (i *Instance) xqd_req_body_downstream_get(request_handle_out int32, body_handle_out int32) int32 {
	// Convert the downstream request into a (request, body) handle pair
	rhid, rh := i.requests.New()
	rh.Request = i.ds_request.Clone(context.Background())

	// downstream requests don't have host or scheme on the URL, but we need it
	rh.URL.Host = i.ds_request.Host

	if i.secureFn(i.ds_request) {
		rh.URL.Scheme = "https"
		rh.Header.Set("fastly-ssl", "1")
	} else {
		rh.URL.Scheme = "http"
	}

	// Capture original header names in their original order for downstream_original_header_names
	// Convert to lowercase to match HTTP/2 convention (headers are case-insensitive)
	// Sort alphabetically since Go map iteration order is non-deterministic
	rh.originalHeaders = make([]string, 0, len(i.ds_request.Header))
	for name := range i.ds_request.Header {
		rh.originalHeaders = append(rh.originalHeaders, strings.ToLower(name))
	}
	// Sort to ensure consistent ordering
	sort.Strings(rh.originalHeaders)

	// Capture TLS connection state if the request was over TLS
	if i.ds_request.TLS != nil {
		rh.tlsState = i.ds_request.TLS
	}

	// NOTE: Originally, we setup the body handle using `bodies.NewReader(rh.Body)`, but there is
	// a bug when the *new* request (rh) is sent via subrequest where the subrequest target doesn't
	// get the body. I don't know why. This "solves" the problem for fastlike, at least
	// temporarily.
	bhid, bh := i.bodies.NewBuffer()
	if i.ds_request.Body != nil {
		_, _ = io.Copy(bh, i.ds_request.Body)
		_ = i.ds_request.Body.Close()
	}

	i.memory.PutUint32(uint32(rhid), int64(request_handle_out))
	i.memory.PutUint32(uint32(bhid), int64(body_handle_out))

	// Store the downstream request handle for implicit downstream request operations
	i.downstreamRequestHandle = int32(rhid)

	i.abilog.Printf("req_body_downstream_get: rh=%d bh=%d", rhid, bhid)

	return XqdStatusOK
}

func (i *Instance) xqd_resp_send_downstream(whandle int32, bhandle int32, stream int32) int32 {
	if stream != 0 {
		i.abilog.Printf("resp_send_downstream: streaming unsupported")
		return XqdErrUnsupported
	}

	w, b := i.responses.Get(int(whandle)), i.bodies.Get(int(bhandle))
	if w == nil {
		i.abilog.Printf("resp_send_downstream: invalid response handle %d", whandle)
		return XqdErrInvalidHandle
	} else if b == nil {
		i.abilog.Printf("resp_send_downstream: invalid body handle %d", bhandle)
		return XqdErrInvalidHandle
	}
	defer func() { _ = b.Close() }()

	for k, v := range w.Header {
		i.ds_response.Header()[k] = v
	}

	i.ds_response.WriteHeader(w.StatusCode)

	_, err := io.Copy(i.ds_response, b)
	if err != nil {
		i.abilog.Printf("resp_send_downstream: copy err, got %s", err.Error())
		return XqdError
	}

	return XqdStatusOK
}

func (i *Instance) xqd_req_downstream_client_ip_addr(octets_out int32, nwritten_out int32) int32 {
	ip := net.ParseIP(strings.SplitN(i.ds_request.RemoteAddr, ":", 2)[0])
	i.abilog.Printf("req_downstream_client_ip_addr: remoteaddr=%s, ip=%q\n", i.ds_request.RemoteAddr, ip)

	// If there's no good IP on the incoming request, we can exit early
	if ip == nil {
		return XqdStatusOK
	}

	// Convert to 4-byte representation if it's an IPv4 address
	// This avoids writing IPv6-mapped IPv4 addresses (::ffff:x.x.x.x)
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
	}

	// Write the IP to memory. net.IP is implemented a byte slice, which we can
	// write directly out
	nwritten, err := i.memory.WriteAt(ip, int64(octets_out))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))

	return XqdStatusOK
}

// xqd_req_downstream_server_ip_addr returns the server's IP address that received the downstream request
// For local testing, this would be the listening address (e.g., 127.0.0.1 or 0.0.0.0)
func (i *Instance) xqd_req_downstream_server_ip_addr(octets_out int32, nwritten_out int32) int32 {
	i.abilog.Printf("req_downstream_server_ip_addr")

	// For local testing, return localhost IPv4 address
	// In production, this would be the actual server IP
	// Use direct byte slice to ensure 4-byte IPv4 representation
	ip := []byte{127, 0, 0, 1}

	// Write the IP to memory
	nwritten, err := i.memory.WriteAt(ip, int64(octets_out))
	if err != nil {
		return XqdError
	}

	i.memory.PutUint32(uint32(nwritten), int64(nwritten_out))
	return XqdStatusOK
}

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

func p(l *log.Logger, name string, args ...int32) {
	xs := []string{}
	for _, x := range args {
		xs = append(xs, fmt.Sprintf("%d", x))
	}

	l.Printf("[STUB] %s: args=%q\n", name, xs)
}

func (i *Instance) wasm1(name string) func(a int32) int32 {
	return func(a int32) int32 {
		p(i.abilog, name, a)
		return 5
	}
}

func (i *Instance) wasm3(name string) func(a, b, c int32) int32 {
	return func(a, b, c int32) int32 {
		p(i.abilog, name, a, b, c)
		return 5
	}
}

func (i *Instance) wasm6(name string) func(a, b, c, d, e, f int32) int32 {
	return func(a, b, c, d, e, f int32) int32 {
		p(i.abilog, name, a, b, c, d, e, f)
		return 5
	}
}
