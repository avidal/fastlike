package fastlike

import (
	"context"
	"net/http"
	"strings"
)

// maxReasonPhraseLen is the longest phrase we keep, same as production.
const maxReasonPhraseLen = 8 * 1024

// canonicalReason returns the standard phrase for a status code, or an empty string if there is none.
//
// Production uses the names from the Rust http crate, and four of them differ from Go's.
func canonicalReason(code int) string {
	switch code {
	case http.StatusNonAuthoritativeInfo:
		return "Non Authoritative Information"
	case http.StatusRequestEntityTooLarge:
		return "Payload Too Large"
	case http.StatusRequestURITooLong:
		return "URI Too Long"
	case http.StatusRequestedRangeNotSatisfiable:
		return "Range Not Satisfiable"
	}
	return http.StatusText(code)
}

// backendReasonPhrase is the phrase the guest sees on a backend response.
//
// When the origin sends none, or it's HTTP/2, the guest gets the standard phrase instead.
func backendReasonPhrase(resp *http.Response) string {
	if resp.ProtoMajor >= 2 {
		return canonicalReason(resp.StatusCode)
	}
	_, phrase, _ := strings.Cut(resp.Status, " ")
	if phrase == "" {
		return canonicalReason(resp.StatusCode)
	}
	if len(phrase) > maxReasonPhraseLen {
		return strings.Clone(phrase[:maxReasonPhraseLen])
	}
	return phrase
}

// ProxyReasonPhrase is a ModifyResponse hook for httputil.ReverseProxy that lets the guest see the origin's reason phrase.
//
// Without it, the guest only gets the standard phrase for the status code.
func ProxyReasonPhrase(resp *http.Response) error {
	if resp.Request != nil {
		setBackendReasonPhrase(resp.Request.Context(), resp)
	}
	return nil
}

// originReasonPhrase keeps the status next to the phrase.
//
// A wrapper like http.TimeoutHandler can send its own status, and that one must not get the origin's phrase.
type originReasonPhrase struct {
	status int
	phrase string
}

func setBackendReasonPhrase(ctx context.Context, resp *http.Response) {
	if w, ok := ctx.Value(backendResponseCtxKey{}).(*backendResponseWriter); ok {
		w.reasonPhrase.Store(&originReasonPhrase{status: resp.StatusCode, phrase: backendReasonPhrase(resp)})
	}
}
