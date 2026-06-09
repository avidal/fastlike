package profile

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProfileStoreRetention(t *testing.T) {
	s := NewProfileStore()
	s.retain = 3

	for i := 0; i < 10; i++ {
		r, _ := http.NewRequest("GET", "http://x/", nil)
		tr := s.NewRequestTrace("modA", r)
		s.CompleteTrace(tr)
	}

	if got, want := len(s.completed), 3; got != want {
		t.Fatalf("retention not enforced: have %d, want %d", got, want)
	}
	// The most recent three should be 8, 9, 10 in some order; Recent() reverses.
	recent := s.Recent(0)
	if recent[0].ReqID != 10 || recent[1].ReqID != 9 || recent[2].ReqID != 8 {
		t.Fatalf("Recent() order wrong: %d, %d, %d", recent[0].ReqID, recent[1].ReqID, recent[2].ReqID)
	}
}

func TestProfileStoreInFlightHandoff(t *testing.T) {
	s := NewProfileStore()
	r, _ := http.NewRequest("POST", "http://x/y", nil)
	tr := s.NewRequestTrace("modZ", r)

	if got := s.InFlight(); len(got) != 1 || got[0].ReqID != tr.ReqID {
		t.Fatalf("trace missing from in-flight set: %+v", got)
	}
	if s.Get(tr.ReqID) != nil {
		t.Fatalf("Get should return nil for in-flight trace, not %+v", s.Get(tr.ReqID))
	}

	s.CompleteTrace(tr)

	if len(s.InFlight()) != 0 {
		t.Fatalf("trace still in in-flight set after completion")
	}
	if got := s.Get(tr.ReqID); got == nil || got.ReqID != tr.ReqID {
		t.Fatalf("Get() failed after completion: %+v", got)
	}
}

func TestProfileStoreNilSafe(t *testing.T) {
	var s *ProfileStore
	// All these must not panic; the nil store represents "profiling disabled".
	if got := s.NewRequestTrace("m", httptest.NewRequest("GET", "/", nil)); got != nil {
		t.Fatalf("expected nil trace from nil store, got %+v", got)
	}
	s.CompleteTrace(&RequestTrace{}) // must not panic
}

func TestModuleIDStability(t *testing.T) {
	a := ModuleIDOf([]byte("hello"))
	b := ModuleIDOf([]byte("hello"))
	c := ModuleIDOf([]byte("hellp"))
	if a != b {
		t.Fatalf("expected stable id for same input, got %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("expected different ids for different inputs, both %s", a)
	}
	if len(a) != 16 {
		t.Fatalf("expected 16-hex-char id, got %d: %q", len(a), a)
	}
}

func TestProfileModeIncludesTrace(t *testing.T) {
	cases := map[ProfileMode]bool{
		ProfileModeOff:      false,
		ProfileModeTrace:    true,
		ProfileModeNative:   true,
		ProfileModeCombined: true,
		ProfileModeDeep:     true,
	}
	for mode, want := range cases {
		if got := mode.IncludesTrace(); got != want {
			t.Errorf("mode %q includesTrace=%v, want %v", mode, got, want)
		}
	}
}
