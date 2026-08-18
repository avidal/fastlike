package fastlike

import "testing"

func TestHTTPHeaderNameCountLimit(t *testing.T) {
	if httpHeaderNameCountAtLimit(maxHTTPHeaderNameCount - 1) {
		t.Fatalf("count below limit was rejected")
	}
	if !httpHeaderNameCountAtLimit(maxHTTPHeaderNameCount) {
		t.Fatalf("limit count was accepted")
	}
	if !httpHeaderNameCountAtLimit(maxHTTPHeaderNameCount + 1) {
		t.Fatalf("count above limit was accepted")
	}
}

func TestHTTPHeaderSyntaxValidators(t *testing.T) {
	for _, valid := range [][]byte{
		[]byte("X-Header"),
		[]byte("!#$%&'*+-.^_`|~AZaz09"),
	} {
		if !validHTTPHeaderName(valid) {
			t.Errorf("header name %q rejected", valid)
		}
	}
	for _, invalid := range [][]byte{
		nil,
		[]byte("Bad Name"),
		[]byte("Bad:Name"),
		{0x80},
	} {
		if validHTTPHeaderName(invalid) {
			t.Errorf("invalid header name %q accepted", invalid)
		}
	}

	for _, valid := range [][]byte{
		nil,
		[]byte("\t printable ~"),
		{0x80, 0xff},
	} {
		if !validHTTPHeaderValue(valid) {
			t.Errorf("header value %q rejected", valid)
		}
	}
	for _, invalid := range [][]byte{
		{0x00},
		{0x1f},
		{'\n'},
		{'\r'},
		{0x7f},
	} {
		if validHTTPHeaderValue(invalid) {
			t.Errorf("invalid header value %q accepted", invalid)
		}
	}
}
