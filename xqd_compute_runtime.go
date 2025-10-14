package fastlike

// xqd_compute_runtime_get_vcpu_ms returns the amount of active CPU time (in milliseconds)
// that has been consumed by the WebAssembly guest during request processing.
//
// This is NOT wall clock time - it only includes time when the CPU is actively executing
// guest code. Time spent waiting for I/O (e.g., backend requests) is not included.
//
// The function internally tracks time in microseconds for accuracy, but returns milliseconds
// to reduce timing attack surface (following Viceroy's approach).
//
// Signature: (vcpu_ms_out: *mut u64) -> fastly_status
func (i *Instance) xqd_compute_runtime_get_vcpu_ms(vcpu_ms_out int32) int32 {
	i.abilog.Printf("compute_runtime_get_vcpu_ms: vcpu_ms_out=%d", vcpu_ms_out)

	// Load accumulated CPU time in microseconds
	microseconds := i.activeCpuTimeUs.Load()

	// Convert to milliseconds
	// We track internally in microseconds because Go's time precision is high,
	// but we return milliseconds to minimize timing attack vectors.
	milliseconds := microseconds / 1000

	i.abilog.Printf("compute_runtime_get_vcpu_ms: returning %d ms (%d us)", milliseconds, microseconds)

	// Write milliseconds to guest memory as u64
	i.memory.WriteUint64(vcpu_ms_out, milliseconds)

	return XqdStatusOK
}
