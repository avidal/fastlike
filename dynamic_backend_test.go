package fastlike

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Guest memory layout used by every test in this file.
const (
	dynNameAddr   int32 = 0
	dynTargetAddr int32 = 128
	dynCfgAddr    int32 = 512
	dynStrAddr    int32 = 1024
)

// Offsets of the dynamic_backend_config fields poked by tests.
const (
	cfgHostOverridePtr = 0
	cfgHostOverrideLen = 4
	cfgSSLMinVersion   = 20
	cfgCertHostnamePtr = 28
	cfgCertHostnameLen = 32
	cfgCACertPtr       = 36
	cfgCACertLen       = 40
	cfgSNIHostnamePtr  = 52
	cfgSNIHostnameLen  = 56
	cfgClientCertPtr   = 60
	cfgClientCertLen   = 64
	cfgClientKey       = 68
	cfgMaxConnections  = 92
	cfgMaxUse          = 96
	cfgMaxLifetimeMs   = 100
)

func newDynInstance() *Instance {
	return &Instance{
		backends:            map[string]*Backend{},
		memory:              &Memory{ByteMemory(make([]byte, 256*1024))},
		abilog:              log.New(io.Discard, "", 0),
		secretHandles:       &SecretHandles{},
		requests:            &RequestHandles{},
		bodies:              &BodyHandles{},
		responses:           &ResponseHandles{},
		pendingRequests:     &PendingRequestHandles{},
		requestPromises:     &RequestPromiseHandles{},
		kvStores:            &KVStoreHandles{},
		kvLookups:           &KVStoreLookupHandles{},
		kvInserts:           &KVStoreInsertHandles{},
		kvDeletes:           &KVStoreDeleteHandles{},
		kvLists:             &KVStoreListHandles{},
		secretStoreHandles:  &SecretStoreHandles{},
		cacheHandles:        &CacheHandles{},
		cacheBusyHandles:    &CacheBusyHandles{},
		cacheReplaceHandles: &CacheReplaceHandles{},
		aclHandles:          &AclHandles{},
		asyncItems:          &AsyncItemHandles{},
	}
}

func pokeCfgU32(inst *Instance, fieldOffset int64, v uint32) {
	inst.memory.PutUint32(v, int64(dynCfgAddr)+fieldOffset)
}

// pokeCfgString writes s into the string data region and points the given
// ptr/len config fields at it, returning the next free data address.
func pokeCfgString(t *testing.T, inst *Instance, ptrOffset, lenOffset int64, addr int32, s string) int32 {
	t.Helper()
	writeStr(t, inst, int64(addr), s)
	pokeCfgU32(inst, ptrOffset, uint32(addr))
	pokeCfgU32(inst, lenOffset, uint32(len(s)))
	return addr + int32(len(s))
}

func registerDyn(t *testing.T, inst *Instance, name, target string, mask uint32) int32 {
	t.Helper()
	nameAddr, nameSize := writeStr(t, inst, int64(dynNameAddr), name)
	targetAddr, targetSize := writeStr(t, inst, int64(dynTargetAddr), target)
	return inst.xqd_req_register_dynamic_backend(nameAddr, nameSize, targetAddr, targetSize, int32(mask), dynCfgAddr)
}

func TestRegisterDynamicBackend_TargetForms(t *testing.T) {
	cases := []struct {
		target string
		mask   uint32
		status int32
		scheme string
		host   string
	}{
		{"origin.example.org:8080", 0, XqdStatusOK, "http", "origin.example.org:8080"},
		{"origin.example.org", BackendConfigOptionsUseSSL, XqdStatusOK, "https", "origin.example.org"},
		{"192.0.2.1:443", BackendConfigOptionsUseSSL, XqdStatusOK, "https", "192.0.2.1:443"},
		{"[2001:db8::1]:443", BackendConfigOptionsUseSSL, XqdStatusOK, "https", "[2001:db8::1]:443"},
		{"https://origin.example.org", 0, XqdErrInvalidArgument, "", ""},
		{"origin.example.org/path", 0, XqdErrInvalidArgument, "", ""},
		{"user@origin.example.org", 0, XqdErrInvalidArgument, "", ""},
		{"origin.example.org:nope", 0, XqdErrInvalidArgument, "", ""},
		{"", 0, XqdErrInvalidArgument, "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			inst := newDynInstance()
			status := registerDyn(t, inst, "origin", tc.target, tc.mask)
			if status != tc.status {
				t.Fatalf("status = %d, want %d", status, tc.status)
			}
			if tc.status != XqdStatusOK {
				if inst.backendExists("origin") {
					t.Fatal("failed registration must not leave a backend behind")
				}
				return
			}
			b := inst.getBackend("origin")
			if b == nil || !b.IsDynamic {
				t.Fatal("expected a dynamic backend")
			}
			if b.URL.Scheme != tc.scheme || b.URL.Host != tc.host {
				t.Errorf("URL = %s://%s, want %s://%s", b.URL.Scheme, b.URL.Host, tc.scheme, tc.host)
			}
			if b.UseSSL != (tc.scheme == "https") {
				t.Errorf("UseSSL = %v with scheme %s", b.UseSSL, tc.scheme)
			}
		})
	}
}

func TestRegisterDynamicBackend_MaskValidation(t *testing.T) {
	cases := []struct {
		name   string
		mask   uint32
		status int32
	}{
		{"reserved bit", BackendConfigOptionsReserved, XqdErrInvalidArgument},
		{"unknown bit", 1 << 19, XqdErrInvalidArgument},
		{"healthcheck accepted", BackendConfigOptionsHealthcheck, XqdStatusOK},
		{"prefer ipv4 accepted", BackendConfigOptionsPreferIPv4, XqdStatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := newDynInstance()
			if status := registerDyn(t, inst, "origin", "origin.example.org", tc.mask); status != tc.status {
				t.Errorf("status = %d, want %d", status, tc.status)
			}
		})
	}
}

func TestRegisterDynamicBackend_NameCollision(t *testing.T) {
	inst := newDynInstance()
	inst.backends["static"] = &Backend{Name: "static"}

	if status := registerDyn(t, inst, "static", "origin.example.org", 0); status != XqdError {
		t.Errorf("collision with static backend: status = %d, want %d", status, XqdError)
	}
	if status := registerDyn(t, inst, "origin", "origin.example.org", 0); status != XqdStatusOK {
		t.Fatalf("first registration: status = %d", status)
	}
	if status := registerDyn(t, inst, "origin", "origin.example.org", 0); status != XqdError {
		t.Errorf("re-registration: status = %d, want %d", status, XqdError)
	}
}

func TestRegisterDynamicBackend_OptionValidation(t *testing.T) {
	t.Run("empty host override fails", func(t *testing.T) {
		inst := newDynInstance()
		pokeCfgU32(inst, cfgHostOverridePtr, uint32(dynStrAddr))
		pokeCfgU32(inst, cfgHostOverrideLen, 0)
		status := registerDyn(t, inst, "origin", "origin.example.org", BackendConfigOptionsHostOverride)
		if status != XqdErrInvalidArgument {
			t.Errorf("status = %d, want %d", status, XqdErrInvalidArgument)
		}
	})

	t.Run("oversized cert hostname fails", func(t *testing.T) {
		inst := newDynInstance()
		pokeCfgU32(inst, cfgCertHostnamePtr, uint32(dynStrAddr))
		pokeCfgU32(inst, cfgCertHostnameLen, 2048)
		status := registerDyn(t, inst, "origin", "origin.example.org", BackendConfigOptionsCertHostname)
		if status != XqdErrInvalidArgument {
			t.Errorf("status = %d, want %d", status, XqdErrInvalidArgument)
		}
	})

	t.Run("host override applied", func(t *testing.T) {
		inst := newDynInstance()
		pokeCfgString(t, inst, cfgHostOverridePtr, cfgHostOverrideLen, dynStrAddr, "vhost.example.org")
		status := registerDyn(t, inst, "origin", "origin.example.org", BackendConfigOptionsHostOverride)
		if status != XqdStatusOK {
			t.Fatalf("status = %d", status)
		}
		if got := inst.getBackend("origin").OverrideHost; got != "vhost.example.org" {
			t.Errorf("OverrideHost = %q", got)
		}
	})

	t.Run("empty sni disables the extension", func(t *testing.T) {
		inst := newDynInstance()
		pokeCfgU32(inst, cfgSNIHostnamePtr, uint32(dynStrAddr))
		pokeCfgU32(inst, cfgSNIHostnameLen, 0)
		mask := BackendConfigOptionsUseSSL | BackendConfigOptionsSNIHostname
		if status := registerDyn(t, inst, "origin", "origin.example.org", mask); status != XqdStatusOK {
			t.Fatalf("status = %d", status)
		}
		b := inst.getBackend("origin")
		if !b.DisableSNI || b.SNIHostname != "" {
			t.Errorf("DisableSNI = %v, SNIHostname = %q", b.DisableSNI, b.SNIHostname)
		}
	})

	t.Run("garbage ca certificate fails", func(t *testing.T) {
		inst := newDynInstance()
		pokeCfgString(t, inst, cfgCACertPtr, cfgCACertLen, dynStrAddr, "not a pem")
		mask := BackendConfigOptionsUseSSL | BackendConfigOptionsCACert
		if status := registerDyn(t, inst, "origin", "origin.example.org", mask); status != XqdErrInvalidArgument {
			t.Errorf("status = %d, want %d", status, XqdErrInvalidArgument)
		}
	})

	t.Run("pooling limits applied", func(t *testing.T) {
		inst := newDynInstance()
		pokeCfgU32(inst, cfgMaxConnections, 7)
		pokeCfgU32(inst, cfgMaxUse, 3)
		pokeCfgU32(inst, cfgMaxLifetimeMs, 9000)
		if status := registerDyn(t, inst, "origin", "origin.example.org", BackendConfigOptionsPoolingLimits); status != XqdStatusOK {
			t.Fatalf("status = %d", status)
		}
		b := inst.getBackend("origin")
		if b.MaxConnections != 7 || b.MaxUse != 3 || b.MaxLifetimeMs != 9000 {
			t.Errorf("pooling limits = %d/%d/%d", b.MaxConnections, b.MaxUse, b.MaxLifetimeMs)
		}
	})
}

func TestRegisterDynamicBackend_TLSVersionValidation(t *testing.T) {
	t.Run("out of range version fails even without its option bit", func(t *testing.T) {
		inst := newDynInstance()
		pokeCfgU32(inst, cfgSSLMinVersion, 7)
		if status := registerDyn(t, inst, "origin", "origin.example.org", 0); status != XqdErrInvalidArgument {
			t.Errorf("status = %d, want %d", status, XqdErrInvalidArgument)
		}
	})

	t.Run("tls 1.0 minimum is honored", func(t *testing.T) {
		inst := newDynInstance()
		pokeCfgU32(inst, cfgSSLMinVersion, TLSv10)
		mask := BackendConfigOptionsUseSSL | BackendConfigOptionsSSLMinVersion
		if status := registerDyn(t, inst, "origin", "origin.example.org", mask); status != XqdStatusOK {
			t.Fatalf("status = %d", status)
		}
		b := inst.getBackend("origin")
		if !b.SSLMinVersionSet || b.SSLMinVersion != TLSv10 {
			t.Errorf("SSLMinVersion = %d set=%v", b.SSLMinVersion, b.SSLMinVersionSet)
		}
		if got := b.Transport.TLSClientConfig.MinVersion; got != tls.VersionTLS10 {
			t.Errorf("transport MinVersion = %#x, want VersionTLS10", got)
		}
	})
}

func TestMaxConnectionsScoping(t *testing.T) {
	static := &Backend{Name: "static", MaxConnections: 4}
	if got := static.CreateTransport().MaxConnsPerHost; got != 0 {
		t.Errorf("static backend MaxConnsPerHost = %d, want 0", got)
	}
	dynamic := &Backend{Name: "dyn", IsDynamic: true, MaxConnections: 4}
	if got := dynamic.CreateTransport().MaxConnsPerHost; got != 4 {
		t.Errorf("dynamic backend MaxConnsPerHost = %d, want 4", got)
	}
}

func TestBetweenBytesBody(t *testing.T) {
	pr, pw := io.Pipe()
	body := newBetweenBytesBody(pr, 50*time.Millisecond)
	go func() {
		_, _ = pw.Write([]byte("x"))
		// Stall past the between-bytes timeout without closing the pipe.
	}()

	buf := make([]byte, 1)
	if n, err := body.Read(buf); n != 1 || err != nil {
		t.Fatalf("first read = (%d, %v)", n, err)
	}

	start := time.Now()
	_, err := body.Read(buf)
	if err == nil {
		t.Fatal("stalled read did not fail")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stalled read took %v", elapsed)
	}
	_ = body.Close()
	_ = pw.Close()
}

func TestRegisterDynamicBackend_GRPCTransport(t *testing.T) {
	inst := newDynInstance()
	if status := registerDyn(t, inst, "origin", "origin.example.org:50051", BackendConfigOptionsGRPC); status != XqdStatusOK {
		t.Fatalf("status = %d", status)
	}
	b := inst.getBackend("origin")
	if !b.GRPC {
		t.Fatal("GRPC flag not recorded")
	}
	p := b.Transport.Protocols
	if p == nil || !p.HTTP2() || !p.UnencryptedHTTP2() || p.HTTP1() {
		t.Errorf("plaintext gRPC backend must be h2c-only, got %v", p)
	}
}

func TestResetClearsDynamicBackends(t *testing.T) {
	inst := newDynInstance()
	inst.backends["static"] = &Backend{Name: "static"}

	if status := registerDyn(t, inst, "origin", "origin.example.org", 0); status != XqdStatusOK {
		t.Fatalf("status = %d", status)
	}

	inst.reset()

	if inst.backendExists("origin") {
		t.Error("dynamic backend survived reset")
	}
	if !inst.backendExists("static") {
		t.Error("static backend must survive reset")
	}

	inst.memory = &Memory{ByteMemory(make([]byte, 256*1024))}
	if status := registerDyn(t, inst, "origin", "origin.example.org", 0); status != XqdStatusOK {
		t.Errorf("re-registration after reset: status = %d", status)
	}
}

// startSNIRecordingTLSServer starts a TLS server that records the SNI value
// of each ClientHello and echoes the request's Host header.
func startSNIRecordingTLSServer(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	var lastSNI string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "host=%s", r.Host)
	}))
	srv.TLS = &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			mu.Lock()
			lastSNI = hello.ServerName
			mu.Unlock()
			return nil, nil
		},
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, func() string {
		mu.Lock()
		defer mu.Unlock()
		return lastSNI
	}
}

func serverCertPEM(srv *httptest.Server) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
}

// registerTLSBackend registers a dynamic backend against srv with the given
// cert and SNI hostnames. An sni of "-" leaves the SNI option out entirely;
// an empty sni sets the option with a zero length, which disables SNI.
func registerTLSBackend(t *testing.T, inst *Instance, srv *httptest.Server, certHost, sni string) *Backend {
	t.Helper()
	mask := BackendConfigOptionsUseSSL | BackendConfigOptionsCACert
	next := pokeCfgString(t, inst, cfgCACertPtr, cfgCACertLen, dynStrAddr, serverCertPEM(srv))
	if certHost != "" {
		mask |= BackendConfigOptionsCertHostname
		next = pokeCfgString(t, inst, cfgCertHostnamePtr, cfgCertHostnameLen, next, certHost)
	}
	if sni != "-" {
		mask |= BackendConfigOptionsSNIHostname
		if sni == "" {
			pokeCfgU32(inst, cfgSNIHostnamePtr, uint32(next))
			pokeCfgU32(inst, cfgSNIHostnameLen, 0)
		} else {
			pokeCfgString(t, inst, cfgSNIHostnamePtr, cfgSNIHostnameLen, next, sni)
		}
	}
	if status := registerDyn(t, inst, "origin", srv.Listener.Addr().String(), mask); status != XqdStatusOK {
		t.Fatalf("registration failed: status = %d", status)
	}
	return inst.getBackend("origin")
}

func callThroughBackend(t *testing.T, b *Backend, uri string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", uri, nil)
	rec := httptest.NewRecorder()
	b.Handler.ServeHTTP(rec, req)
	return rec
}

func TestDynamicBackendTLS_SNIAndVerification(t *testing.T) {
	// The httptest certificate is valid for example.com, so verification
	// must pass when the cert hostname says example.com even though the
	// connection goes to 127.0.0.1.
	t.Run("matching cert and sni", func(t *testing.T) {
		srv, sniOf := startSNIRecordingTLSServer(t)
		b := registerTLSBackend(t, newDynInstance(), srv, "example.com", "-")
		rec := callThroughBackend(t, b, "https://example.com/hello")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if got := sniOf(); got != "example.com" {
			t.Errorf("server saw SNI %q, want example.com", got)
		}
		if rec.Body.String() != "host=example.com" {
			t.Errorf("origin saw %q, want host=example.com", rec.Body.String())
		}
	})

	t.Run("distinct sni and cert hostname", func(t *testing.T) {
		srv, sniOf := startSNIRecordingTLSServer(t)
		b := registerTLSBackend(t, newDynInstance(), srv, "example.com", "front.test")
		rec := callThroughBackend(t, b, "https://example.com/hello")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if got := sniOf(); got != "front.test" {
			t.Errorf("server saw SNI %q, want front.test", got)
		}
	})

	// The verification fallback chain is cert_hostname, then sni_hostname,
	// then the target host. A certificate valid only for the SNI name (not
	// for 127.0.0.1) must verify when only sni_hostname is given.
	t.Run("sni only drives verification", func(t *testing.T) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(2),
			Subject:               pkix.Name{CommonName: "onlysni.test"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(time.Hour),
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			DNSNames:              []string{"onlysni.test"},
			BasicConstraintsValid: true,
			IsCA:                  true,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.TLS = &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		}
		srv.StartTLS()
		t.Cleanup(srv.Close)

		b := registerTLSBackend(t, newDynInstance(), srv, "", "onlysni.test")
		rec := callThroughBackend(t, b, "https://onlysni.test/hello")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
	})

	t.Run("disabled sni", func(t *testing.T) {
		srv, sniOf := startSNIRecordingTLSServer(t)
		b := registerTLSBackend(t, newDynInstance(), srv, "example.com", "")
		rec := callThroughBackend(t, b, "https://example.com/hello")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if got := sniOf(); got != "" {
			t.Errorf("server saw SNI %q, want none", got)
		}
	})

	t.Run("verification failure surfaces as send error", func(t *testing.T) {
		srv, _ := startSNIRecordingTLSServer(t)
		b := registerTLSBackend(t, newDynInstance(), srv, "wrong.test", "-")
		rec := callThroughBackend(t, b, "https://example.com/hello")
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
	})

	t.Run("host override wins", func(t *testing.T) {
		srv, _ := startSNIRecordingTLSServer(t)
		inst := newDynInstance()
		mask := BackendConfigOptionsUseSSL | BackendConfigOptionsCACert |
			BackendConfigOptionsCertHostname | BackendConfigOptionsHostOverride
		next := pokeCfgString(t, inst, cfgCACertPtr, cfgCACertLen, dynStrAddr, serverCertPEM(srv))
		next = pokeCfgString(t, inst, cfgCertHostnamePtr, cfgCertHostnameLen, next, "example.com")
		pokeCfgString(t, inst, cfgHostOverridePtr, cfgHostOverrideLen, next, "vhost.example.org")
		if status := registerDyn(t, inst, "origin", srv.Listener.Addr().String(), mask); status != XqdStatusOK {
			t.Fatalf("status = %d", status)
		}
		rec := callThroughBackend(t, inst.getBackend("origin"), "https://example.com/hello")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		if rec.Body.String() != "host=vhost.example.org" {
			t.Errorf("origin saw %q, want host=vhost.example.org", rec.Body.String())
		}
	})
}

func TestDynamicBackendTLS_ClientCertificate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fastlike test client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	var mu sync.Mutex
	var sawClientCert bool
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawClientCert = r.TLS != nil && len(r.TLS.PeerCertificates) > 0
		mu.Unlock()
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	inst := newDynInstance()
	keyHandle := inst.secretHandles.New(keyPEM)

	mask := BackendConfigOptionsUseSSL | BackendConfigOptionsCACert |
		BackendConfigOptionsCertHostname | BackendConfigOptionsClientCert
	next := pokeCfgString(t, inst, cfgCACertPtr, cfgCACertLen, dynStrAddr, serverCertPEM(srv))
	next = pokeCfgString(t, inst, cfgCertHostnamePtr, cfgCertHostnameLen, next, "example.com")
	pokeCfgString(t, inst, cfgClientCertPtr, cfgClientCertLen, next, string(certPEM))
	pokeCfgU32(inst, cfgClientKey, uint32(keyHandle))

	if status := registerDyn(t, inst, "origin", srv.Listener.Addr().String(), mask); status != XqdStatusOK {
		t.Fatalf("status = %d", status)
	}
	rec := callThroughBackend(t, inst.getBackend("origin"), "https://example.com/hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawClientCert {
		t.Error("origin did not receive the client certificate")
	}

	t.Run("invalid key handle fails", func(t *testing.T) {
		inst := newDynInstance()
		pokeCfgString(t, inst, cfgClientCertPtr, cfgClientCertLen, dynStrAddr, string(certPEM))
		pokeCfgU32(inst, cfgClientKey, 99)
		status := registerDyn(t, inst, "origin", "origin.example.org", BackendConfigOptionsUseSSL|BackendConfigOptionsClientCert)
		if status != XqdErrInvalidArgument {
			t.Errorf("status = %d, want %d", status, XqdErrInvalidArgument)
		}
	})
}
