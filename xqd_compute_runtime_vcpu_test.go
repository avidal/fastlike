package fastlike

import (
	"io"
	"log"
	"testing"
	"time"
)

func newVcpuTestInstance() *Instance {
	return &Instance{
		memory: &Memory{ByteMemory(make([]byte, 64))},
		abilog: log.New(io.Discard, "", 0),
	}
}

func readVcpuMs(t *testing.T, inst *Instance) uint64 {
	t.Helper()
	const out int32 = 0
	if status := inst.xqd_compute_runtime_get_vcpu_ms(out); status != XqdStatusOK {
		t.Fatalf("status = %d, want %d", status, XqdStatusOK)
	}
	return inst.memory.Uint64(int64(out))
}

// A guest that computes without blocking must still see its CPU time grow.
func TestVcpuMsIncludesTheRunningStretch(t *testing.T) {
	inst := newVcpuTestInstance()
	inst.executionStartTime = time.Now().Add(-25 * time.Millisecond)

	if got := readVcpuMs(t, inst); got < 25 {
		t.Errorf("vcpu ms = %d, want at least 25 while the guest is running", got)
	}
}

func TestVcpuMsAddsTheRunningStretchToPausedTime(t *testing.T) {
	inst := newVcpuTestInstance()
	inst.activeCpuTimeUs.Store(40_000)
	inst.executionStartTime = time.Now().Add(-10 * time.Millisecond)

	if got := readVcpuMs(t, inst); got < 50 {
		t.Errorf("vcpu ms = %d, want at least 50", got)
	}
}

func TestVcpuMsWhilePausedIsTheAccumulatedTime(t *testing.T) {
	inst := newVcpuTestInstance()
	inst.activeCpuTimeUs.Store(12_345)

	if got := readVcpuMs(t, inst); got != 12 {
		t.Errorf("vcpu ms = %d, want 12", got)
	}
}
