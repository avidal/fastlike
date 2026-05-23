package fastlike

import (
	"net/http"
	"strings"
	"testing"
)

func TestSummarizeHeadersEmpty(t *testing.T) {
	if got := summarizeHeaders(nil); got != nil {
		t.Errorf("nil header should return nil, got %+v", got)
	}
	if got := summarizeHeaders(http.Header{}); got != nil {
		t.Errorf("empty header should return nil, got %+v", got)
	}
}

func TestSummarizeHeadersCanonicalization(t *testing.T) {
	h := http.Header{}
	h.Add("user-agent", "test/1")
	got := summarizeHeaders(h)
	if len(got) != 1 {
		t.Fatalf("len: %d", len(got))
	}
	if got[0].Name != "User-Agent" {
		t.Errorf("name: %q, want User-Agent", got[0].Name)
	}
	if got[0].Count != 1 {
		t.Errorf("count: %d", got[0].Count)
	}
}

func TestSummarizeHeadersMultiValue(t *testing.T) {
	h := http.Header{}
	h.Add("x-custom", "a")
	h.Add("x-custom", "bb")
	h.Add("x-custom", "ccc")
	got := summarizeHeaders(h)
	if len(got) != 1 {
		t.Fatalf("len: %d", len(got))
	}
	if got[0].Count != 3 {
		t.Errorf("count: %d, want 3", got[0].Count)
	}
	// Bytes: 3 * (len("X-Custom")=8 + 4 framing) + lens(1+2+3) = 36 + 6 = 42.
	want := 3*(len("X-Custom")+4) + 1 + 2 + 3
	if got[0].Bytes != want {
		t.Errorf("bytes: %d, want %d", got[0].Bytes, want)
	}
}

func TestSummarizeHeadersRedactsDenyList(t *testing.T) {
	// Every casing variant must hit the redaction path.
	cases := []string{"Cookie", "COOKIE", "cookie", "CoOkIe", "set-cookie", "Set-Cookie",
		"authorization", "Authorization", "AUTHORIZATION",
		"proxy-authorization", "x-api-key", "X-API-Key", "X-Api-Key",
		"proxy-authenticate", "www-authenticate"}
	for _, name := range cases {
		h := http.Header{}
		h.Add(name, "scary-value-must-not-leak")
		got := summarizeHeaders(h)
		if len(got) != 1 {
			t.Fatalf("%q: len %d", name, len(got))
		}
		if got[0].Name != redactedHeaderPlaceholder {
			t.Errorf("%q: name %q, want %q", name, got[0].Name, redactedHeaderPlaceholder)
		}
		// Value must never appear in any field.
		if strings.Contains(got[0].Name, "scary-value") {
			t.Errorf("%q: value leaked into Name", name)
		}
	}
}

func TestSummarizeHeadersRedactedCountsPreservedSeparately(t *testing.T) {
	// Multiple distinct redacted headers each get their own row, not
	// collapsed into one, so the operator sees "two large redacted
	// headers" instead of "one summed redacted total".
	h := http.Header{}
	h.Add("Cookie", "a=1")
	h.Add("Authorization", "Bearer xyz")
	got := summarizeHeaders(h)
	if len(got) != 2 {
		t.Fatalf("len: %d, want 2 distinct redacted rows", len(got))
	}
	for _, row := range got {
		if row.Name != redactedHeaderPlaceholder {
			t.Errorf("redacted row name wrong: %q", row.Name)
		}
	}
}

func TestSummarizeHeadersMixedRedactedAndPublic(t *testing.T) {
	h := http.Header{}
	h.Add("Content-Type", "application/json")
	h.Add("Authorization", "Bearer secret-token-PROHIBITED")
	h.Add("User-Agent", "ua/1")
	got := summarizeHeaders(h)
	if len(got) != 3 {
		t.Fatalf("len: %d", len(got))
	}
	// "<redacted>" sorts before any capital-letter canonical name.
	if got[0].Name != redactedHeaderPlaceholder {
		t.Errorf("first row should be redacted, got %q", got[0].Name)
	}
	// And the scary value must NEVER appear in the byte representation.
	for _, row := range got {
		if strings.Contains(row.Name, "secret-token") {
			t.Errorf("value leaked: %+v", row)
		}
	}
}

func TestSummarizeHeadersStableSort(t *testing.T) {
	h := http.Header{}
	h.Add("Z-Header", "z")
	h.Add("A-Header", "a")
	h.Add("M-Header", "m")
	got := summarizeHeaders(h)
	if len(got) != 3 || got[0].Name != "A-Header" || got[1].Name != "M-Header" || got[2].Name != "Z-Header" {
		t.Errorf("sort wrong: %+v", got)
	}
}

func TestHeaderAggregateTotals(t *testing.T) {
	summaries := []HeaderSummary{
		{Name: "A", Count: 1, Bytes: 10},
		{Name: "B", Count: 3, Bytes: 50},
		{Name: redactedHeaderPlaceholder, Count: 1, Bytes: 100},
	}
	count, bytes := headerAggregateTotals(summaries)
	if count != 5 {
		t.Errorf("count: %d, want 5", count)
	}
	if bytes != 160 {
		t.Errorf("bytes: %d, want 160", bytes)
	}
}

func TestSummarizeHeadersValuesNeverEscape(t *testing.T) {
	// Cross-cutting test: every reachable field on every row must be
	// inspectable for value leakage. The "bytes" field is an integer
	// so it cannot carry a value substring; the test focuses on Name.
	scaryValues := []string{
		"ya29.OAUTH_TOKEN_REAL",
		"Bearer SECRET_BEARER_PROHIBITED",
		"session=abc; HttpOnly",
		"hunter2",
	}
	h := http.Header{}
	for i, v := range scaryValues {
		h.Add("Authorization", v)
		h.Add("Cookie", v)
		if i == 0 {
			// Also include a non-redacted header with a scary value
			// in it — the value still must not leak.
			h.Add("X-Custom-Tracking", v)
		}
	}
	got := summarizeHeaders(h)
	for _, row := range got {
		for _, v := range scaryValues {
			if strings.Contains(row.Name, v) {
				t.Errorf("value %q leaked into Name %q", v, row.Name)
			}
		}
	}
}
