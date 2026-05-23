package fastlike

import (
	"strings"
	"testing"
	"time"
)

func TestHostcallNameIndex(t *testing.T) {
	cases := []string{
		"body_downstream_get",
		"method_get",
		"uri_set",
		"send",
		"send_async",
		"pending_req_wait",
		"send_downstream",
		"write",
	}
	for _, name := range cases {
		idx := hostcallNameIndex(name)
		if idx == 0 {
			t.Fatalf("hostcallNameIndex(%q) returned unknown sentinel", name)
		}
		if hostcallNames[idx] != name {
			t.Fatalf("hostcallNames[%d]=%q, want %q", idx, hostcallNames[idx], name)
		}
	}
	if got := hostcallNameIndex("not_a_real_hostcall"); got != 0 {
		t.Fatalf("unknown hostcall index = %d, want 0", got)
	}
}

func TestHostcallSpanRecording(t *testing.T) {
	i := &Instance{trace: &RequestTrace{
		WallStart: time.Now().Add(-time.Second),
		Spans:     make([]Span, 0, 2),
	}}
	start := i.startHostcallSpan()
	tags := hostcallTagSlots(10, 20, 30, 40)
	i.finishHostcallSpan(hostcallNameIndex("send"), start, XqdStatusOK, tags)

	if got, want := len(i.trace.Spans), 1; got != want {
		t.Fatalf("span count: got %d, want %d", got, want)
	}
	span := i.trace.Spans[0]
	if hostcallNames[span.NameIdx] != "send" {
		t.Fatalf("span name: got %q", hostcallNames[span.NameIdx])
	}
	if span.RC != XqdStatusOK {
		t.Fatalf("span RC: got %d, want %d", span.RC, XqdStatusOK)
	}
	if span.TagSlots != tags {
		t.Fatalf("span tags: got %+v, want %+v", span.TagSlots, tags)
	}
	if span.Duration <= 0 {
		t.Fatalf("duration not recorded: %d", span.Duration)
	}
	if i.trace.HostcallNanos < span.Duration {
		t.Fatalf("HostcallNanos %d should include duration %d", i.trace.HostcallNanos, span.Duration)
	}
}

func TestHostcallSpanDropCap(t *testing.T) {
	i := &Instance{trace: &RequestTrace{
		WallStart: time.Now(),
		Spans:     make([]Span, 0, 1),
	}}
	idx := hostcallNameIndex("write")
	i.finishHostcallSpan(idx, time.Now(), XqdStatusOK, hostcallTagSlots(1, 2, 3, 4))
	i.finishHostcallSpan(idx, time.Now(), XqdStatusOK, hostcallTagSlots(5, 6, 7, 8))

	if got, want := len(i.trace.Spans), 1; got != want {
		t.Fatalf("span count after cap: got %d, want %d", got, want)
	}
	if got, want := i.trace.Dropped, 1; got != want {
		t.Fatalf("Dropped: got %d, want %d", got, want)
	}
}

func TestHostcallSpanNoopWhenDisabled(t *testing.T) {
	i := &Instance{}
	start := i.startHostcallSpan()
	i.finishHostcallSpan(hostcallNameIndex("send"), start, XqdStatusOK, hostcallTagSlots(1, 2, 3, 4))
	if !start.IsZero() {
		t.Fatalf("disabled trace start = %v, want zero", start)
	}
	if i.trace != nil {
		t.Fatalf("disabled trace should remain nil")
	}
}

func TestMarkHostcallPanic(t *testing.T) {
	i := &Instance{trace: &RequestTrace{}}
	i.markHostcallPanic("send", "boom")
	if i.trace.Outcome != TraceOutcomePanic {
		t.Fatalf("outcome = %s, want panic", i.trace.Outcome)
	}
	if got := strings.Join(i.trace.Notes, "\n"); !strings.Contains(got, "hostcall send panic: boom") {
		t.Fatalf("panic note missing: %q", got)
	}
}

func BenchmarkHostcallSpanRecording(b *testing.B) {
	i := &Instance{trace: &RequestTrace{
		WallStart: time.Now(),
		Spans:     make([]Span, 0, defaultProfileSpanCap),
	}}
	idx := hostcallNameIndex("send")
	tags := hostcallTagSlots(1, 2, 3, 4)

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if len(i.trace.Spans) == cap(i.trace.Spans) {
			i.trace.Spans = i.trace.Spans[:0]
		}
		start := i.startHostcallSpan()
		i.finishHostcallSpan(idx, start, XqdStatusOK, tags)
	}
}

func BenchmarkHostcallSpanDisabled(b *testing.B) {
	i := &Instance{}
	idx := hostcallNameIndex("send")
	tags := hostcallTagSlots(1, 2, 3, 4)

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		start := i.startHostcallSpan()
		i.finishHostcallSpan(idx, start, XqdStatusOK, tags)
	}
}
