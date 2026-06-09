package profile

import (
	"net/http"
	"sort"
)

// HeaderSummary is the per-header deep-mode capture. Name is the
// canonical-case header name (http.CanonicalHeaderKey form) OR
// "<redacted>" when the name appears in the deny list below. Count is
// the number of distinct values the header had; Bytes is the total
// payload (sum of len(name) + len(value) across every value plus a
// constant approximation of the ": " + CRLF framing).
//
// Privacy contract:
//   - The Name field is either an http canonical header name or the
//     literal string "<redacted>". No header values reach this struct.
//   - When a header name appears in the deny list its Name is collapsed
//     to "<redacted>", but Count and Bytes are still recorded so the
//     operator can see "there is a large redacted header" without
//     learning which one. Multiple redacted headers in the same direction
//     produce multiple rows, each with Name=="<redacted>".
type HeaderSummary struct {
	Name  string
	Count int
	Bytes int
}

// redactedHeaderNames is the case-insensitive set of header names whose
// names get collapsed to "<redacted>" in deep-mode summaries. The
// minimum set is the four the plan calls out (Cookie, Set-Cookie,
// Authorization, Proxy-Authorization). The three additions
// (X-Api-Key, Proxy-Authenticate, WWW-Authenticate) cover common
// extension cases an operator might forget to flag.
//
// Stored canonical-cased; lookup goes through
// http.CanonicalHeaderKey on the incoming name so e.g. "cookie",
// "COOKIE", and "Cookie" all redact equivalently.
var redactedHeaderNames = map[string]struct{}{
	http.CanonicalHeaderKey("Cookie"):              {},
	http.CanonicalHeaderKey("Set-Cookie"):          {},
	http.CanonicalHeaderKey("Authorization"):       {},
	http.CanonicalHeaderKey("Proxy-Authorization"): {},
	http.CanonicalHeaderKey("X-Api-Key"):           {},
	http.CanonicalHeaderKey("Proxy-Authenticate"):  {},
	http.CanonicalHeaderKey("WWW-Authenticate"):    {},
}

// redactedHeaderPlaceholder is the literal Name that appears in every
// redacted HeaderSummary. The placeholder is intentionally short and
// invariant so it never communicates which header was redacted.
const redactedHeaderPlaceholder = "<redacted>"

// headerAggregateTotals returns (count, bytes) summed across every
// HeaderSummary in summaries. Used by encoders that want a coarse
// metadata signal rather than per-header detail (Chrome OtherData,
// Firefox deep_metrics marker).
func headerAggregateTotals(summaries []HeaderSummary) (int, int) {
	count, bytes := 0, 0
	for _, h := range summaries {
		count += h.Count
		bytes += h.Bytes
	}
	return count, bytes
}

// SummarizeHeaders walks h and returns a sorted []HeaderSummary,
// redacting names that appear in redactedHeaderNames. Returns nil for
// an empty / nil input so JSON omitempty can drop the field entirely.
// Sort order: by Name ascending; redacted rows sort together under
// "<redacted>" (which sorts before any canonical header name because
// '<' < 'A').
func SummarizeHeaders(h http.Header) []HeaderSummary {
	if len(h) == 0 {
		return nil
	}
	out := make([]HeaderSummary, 0, len(h))
	for name, values := range h {
		canonical := http.CanonicalHeaderKey(name)
		display := canonical
		if _, redacted := redactedHeaderNames[canonical]; redacted {
			display = redactedHeaderPlaceholder
		}
		bytes := 0
		for _, v := range values {
			// Approximate framing overhead: "Name: Value\r\n" plus the
			// name and value lengths. Operators reading these counts
			// typically want a coarse "how big is the header section"
			// signal, not a wire-exact byte count.
			bytes += len(canonical) + len(v) + 4
		}
		out = append(out, HeaderSummary{
			Name:  display,
			Count: len(values),
			Bytes: bytes,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		// Stable secondary sort for predictable goldens when two
		// redacted rows otherwise tie on name.
		return out[i].Bytes > out[j].Bytes
	})
	return out
}
