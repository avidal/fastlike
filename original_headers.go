package fastlike

import (
	"context"
	"net/http"
	"slices"
	"strings"
)

// A guest can ask for the header names of the downstream request as the client sent them: original casing, wire order, one entry per repeat.
// Go's parser keeps none of that, so the names have to be read off the connection before the server parses the request.
// CaptureOriginalHeaders sets that up, and requests that arrive without a capture fall back to a reconstruction.

// originalHeadersCtxKey carries the header names of the request being served.
// Unexported so embedders cannot collide.
type originalHeadersCtxKey struct{}

// originalHeaderSource hands out the header names of one downstream request.
type originalHeaderSource interface {
	claimOriginalHeaderNames(r *http.Request) []string
}

type fixedOriginalHeaders []string

func (f fixedOriginalHeaders) claimOriginalHeaderNames(*http.Request) []string { return f }

// WithOriginalHeaderNames returns a copy of r carrying the header names the client sent.
// It is for embedders that parse HTTP themselves.
func WithOriginalHeaderNames(r *http.Request, names []string) *http.Request {
	ctx := context.WithValue(r.Context(), originalHeadersCtxKey{}, fixedOriginalHeaders(names))
	return r.WithContext(ctx)
}

// claimOriginalHeaderNames takes the captured names of a single request, or nil when nothing captured them.
//
// Call it once per request, before anything can return early.
// A capture hands out its requests in order, so a request that never claims its names shifts every request behind it.
func claimOriginalHeaderNames(r *http.Request) []string {
	source, _ := r.Context().Value(originalHeadersCtxKey{}).(originalHeaderSource)
	if source == nil {
		return nil
	}
	return source.claimOriginalHeaderNames(r)
}

// capturedNamesMatch reports whether names can be the header names of r.
//
// The check keeps a mistake local.
// A stream the scanner misread, or a request some middleware answered on its own, would otherwise hand one request's names to another.
func capturedNamesMatch(names []string, r *http.Request) bool {
	return slices.Equal(lowerSorted(names), lowerSorted(originalHeaderNamesFromRequest(r)))
}

// lowerSorted returns names in the one form two header lists can be compared in.
func lowerSorted(names []string) []string {
	lowered := make([]string, len(names))
	for n, name := range names {
		lowered[n] = strings.ToLower(name)
	}
	slices.Sort(lowered)
	return lowered
}

// originalHeaderNamesFromRequest rebuilds the downstream header names from a request Go has already parsed.
//
// Only the occurrence counts and the headers Go moved into dedicated fields survive parsing, so the casing and the order are a guess.
// Names come back sorted, with Host first, because map order would otherwise change from one request to the next.
func originalHeaderNamesFromRequest(r *http.Request) []string {
	// HTTP/2 and HTTP/3 carry lowercase names on the wire, so the canonical form Go stores would be wrong there.
	lowercase := r.ProtoMajor >= 2
	name := func(canonical string) string {
		if lowercase {
			return strings.ToLower(canonical)
		}
		return canonical
	}

	names := make([]string, 0, len(r.Header)+3)
	appendName := func(canonical string, count int) {
		for range count {
			names = append(names, name(canonical))
		}
	}

	for header, values := range r.Header {
		appendName(header, len(values))
	}

	// Headers the parser consumed on its way through the request.
	if _, ok := r.Header["Transfer-Encoding"]; !ok {
		appendName("Transfer-Encoding", len(r.TransferEncoding))
	}
	if _, ok := r.Header["Trailer"]; !ok && len(r.Trailer) > 0 {
		appendName("Trailer", 1)
	}
	if _, ok := r.Header["Connection"]; !ok && r.Close && r.ProtoMajor == 1 && r.ProtoMinor == 1 {
		// An HTTP/1.1 request only closes the connection when it asked to.
		appendName("Connection", 1)
	}

	slices.Sort(names)

	// Clients put Host first often enough that it makes the better guess.
	if _, ok := r.Header["Host"]; !ok && r.Host != "" {
		names = append([]string{name("Host")}, names...)
	}

	return names
}
