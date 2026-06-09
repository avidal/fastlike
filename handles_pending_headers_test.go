package fastlike

import (
	"net/http"
	"reflect"
	"testing"
)

func TestPendingHeadersInsertOverwrites(t *testing.T) {
	var p PendingHeaders
	p.Insert("X-Foo", "one")
	p.Insert("x-foo", "two") // case-insensitive, replaces

	h := http.Header{}
	p.Apply(h)
	if got := h.Values("X-Foo"); !reflect.DeepEqual(got, []string{"two"}) {
		t.Fatalf("insert overwrite: got %v, want [two]", got)
	}
}

func TestPendingHeadersAppendAccumulates(t *testing.T) {
	var p PendingHeaders
	p.Append("X-Foo", "a")
	p.Append("X-Foo", "b")

	h := http.Header{}
	p.Apply(h)
	if got := h.Values("X-Foo"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("append accumulate: got %v, want [a b]", got)
	}
}

func TestPendingHeadersInsertDropsQueuedAppend(t *testing.T) {
	var p PendingHeaders
	p.Append("X-Foo", "old")
	p.Insert("X-Foo", "new") // discards the queued append

	h := http.Header{}
	p.Apply(h)
	if got := h.Values("X-Foo"); !reflect.DeepEqual(got, []string{"new"}) {
		t.Fatalf("insert-after-append: got %v, want [new]", got)
	}
}

func TestPendingHeadersRemoveCancelsInsertAndAppend(t *testing.T) {
	var p PendingHeaders
	p.Insert("X-Foo", "ins")
	p.Append("X-Foo", "app")
	p.Remove("X-Foo")

	h := http.Header{"X-Foo": {"original"}}
	p.Apply(h)
	if got := h.Values("X-Foo"); len(got) != 0 {
		t.Fatalf("remove should cancel everything: got %v, want []", got)
	}
}

func TestPendingHeadersApplyOrdering(t *testing.T) {
	// Apply runs remove -> insert -> append against an existing response.
	var p PendingHeaders
	p.Insert("X-Foo", "inserted")
	p.Append("X-Foo", "appended")

	h := http.Header{"X-Foo": {"pre-existing"}}
	p.Apply(h)
	if got := h.Values("X-Foo"); !reflect.DeepEqual(got, []string{"inserted", "appended"}) {
		t.Fatalf("ordering: got %v, want [inserted appended]", got)
	}
}

func TestPendingHeadersRemoveThenInsertWins(t *testing.T) {
	var p PendingHeaders
	p.Remove("X-Foo")
	p.Insert("X-Foo", "back") // a later insert un-cancels the removal

	h := http.Header{"X-Foo": {"original"}}
	p.Apply(h)
	if got := h.Values("X-Foo"); !reflect.DeepEqual(got, []string{"back"}) {
		t.Fatalf("remove-then-insert: got %v, want [back]", got)
	}
}

func TestPendingHeadersEmpty(t *testing.T) {
	var p PendingHeaders
	if !p.empty() {
		t.Fatal("fresh PendingHeaders should be empty")
	}
	p.Append("X-Foo", "v")
	if p.empty() {
		t.Fatal("PendingHeaders with a queued append should not be empty")
	}
}
