package fastlike

// xqd_erl_check_rate checks if adding delta to an entry would exceed the rate limit.
// This is a stub implementation for local testing that always returns "not blocked" (0).
// In production, this would check the rate counter and penalty box.
// Returns 0 if not blocked, 1 if blocked.
func (i *Instance) xqd_erl_check_rate(
	rcPtr int32,
	rcLen int32,
	entryPtr int32,
	entryLen int32,
	delta uint32,
	window uint32,
	limit uint32,
	pbPtr int32,
	pbLen int32,
	ttl uint32,
	blockedPtr int32, // out parameter
) int32 {
	i.abilog.Printf("fastly_erl::check_rate (stub)")

	// Read rate counter name (for validation)
	rcBuf := make([]byte, rcLen)
	_, err := i.memory.ReadAt(rcBuf, int64(rcPtr))
	if err != nil {
		return XqdError
	}
	rcName := string(rcBuf)

	// Read penalty box name (for validation)
	pbBuf := make([]byte, pbLen)
	_, err = i.memory.ReadAt(pbBuf, int64(pbPtr))
	if err != nil {
		return XqdError
	}
	pbName := string(pbBuf)

	// Validate that rate counter exists
	rc := i.getRateCounter(rcName)
	if rc == nil {
		i.abilog.Printf("rate counter '%s' not found", rcName)
		return XqdErrInvalidArgument
	}

	// Validate that penalty box exists
	pb := i.getPenaltyBox(pbName)
	if pb == nil {
		i.abilog.Printf("penalty box '%s' not found", pbName)
		return XqdErrInvalidArgument
	}

	// Stub implementation: always return "not blocked" for local testing
	// This matches Viceroy's behavior
	i.memory.PutUint32(0, int64(blockedPtr))

	return XqdStatusOK
}

// xqd_erl_ratecounter_increment increments an entry in the rate counter by delta
func (i *Instance) xqd_erl_ratecounter_increment(
	rcPtr int32,
	rcLen int32,
	entryPtr int32,
	entryLen int32,
	delta uint32,
) int32 {
	i.abilog.Printf("fastly_erl::ratecounter_increment")

	// Read rate counter name
	rcBuf := make([]byte, rcLen)
	_, err := i.memory.ReadAt(rcBuf, int64(rcPtr))
	if err != nil {
		return XqdError
	}
	rcName := string(rcBuf)

	// Read entry
	entryBuf := make([]byte, entryLen)
	_, err = i.memory.ReadAt(entryBuf, int64(entryPtr))
	if err != nil {
		return XqdError
	}
	entry := string(entryBuf)

	// Get rate counter
	rc := i.getRateCounter(rcName)
	if rc == nil {
		i.abilog.Printf("rate counter '%s' not found", rcName)
		return XqdErrInvalidArgument
	}

	// Increment
	rc.Increment(entry, delta)

	return XqdStatusOK
}

// xqd_erl_ratecounter_lookup_rate looks up the current rate for entry in the rate counter for a window
// Returns the rate in requests per second
func (i *Instance) xqd_erl_ratecounter_lookup_rate(
	rcPtr int32,
	rcLen int32,
	entryPtr int32,
	entryLen int32,
	window uint32,
	ratePtr int32, // out parameter
) int32 {
	i.abilog.Printf("fastly_erl::ratecounter_lookup_rate")

	// Read rate counter name
	rcBuf := make([]byte, rcLen)
	_, err := i.memory.ReadAt(rcBuf, int64(rcPtr))
	if err != nil {
		return XqdError
	}
	rcName := string(rcBuf)

	// Read entry
	entryBuf := make([]byte, entryLen)
	_, err = i.memory.ReadAt(entryBuf, int64(entryPtr))
	if err != nil {
		return XqdError
	}
	entry := string(entryBuf)

	// Get rate counter
	rc := i.getRateCounter(rcName)
	if rc == nil {
		i.abilog.Printf("rate counter '%s' not found", rcName)
		return XqdErrInvalidArgument
	}

	// Lookup rate
	rate := rc.LookupRate(entry, window)

	// Write result
	i.memory.PutUint32(rate, int64(ratePtr))

	return XqdStatusOK
}

// xqd_erl_ratecounter_lookup_count looks up the current count for entry in the rate counter for a duration
func (i *Instance) xqd_erl_ratecounter_lookup_count(
	rcPtr int32,
	rcLen int32,
	entryPtr int32,
	entryLen int32,
	duration uint32,
	countPtr int32, // out parameter
) int32 {
	i.abilog.Printf("fastly_erl::ratecounter_lookup_count")

	// Read rate counter name
	rcBuf := make([]byte, rcLen)
	_, err := i.memory.ReadAt(rcBuf, int64(rcPtr))
	if err != nil {
		return XqdError
	}
	rcName := string(rcBuf)

	// Read entry
	entryBuf := make([]byte, entryLen)
	_, err = i.memory.ReadAt(entryBuf, int64(entryPtr))
	if err != nil {
		return XqdError
	}
	entry := string(entryBuf)

	// Get rate counter
	rc := i.getRateCounter(rcName)
	if rc == nil {
		i.abilog.Printf("rate counter '%s' not found", rcName)
		return XqdErrInvalidArgument
	}

	// Lookup count
	count := rc.LookupCount(entry, duration)

	// Write result
	i.memory.PutUint32(count, int64(countPtr))

	return XqdStatusOK
}

// xqd_erl_penaltybox_add adds entry to the penalty box for the duration of TTL (in seconds)
// TTL is truncated to the nearest minute and must be between 1m and 1h
func (i *Instance) xqd_erl_penaltybox_add(
	pbPtr int32,
	pbLen int32,
	entryPtr int32,
	entryLen int32,
	ttl uint32,
) int32 {
	i.abilog.Printf("fastly_erl::penaltybox_add")

	// Read penalty box name
	pbBuf := make([]byte, pbLen)
	_, err := i.memory.ReadAt(pbBuf, int64(pbPtr))
	if err != nil {
		return XqdError
	}
	pbName := string(pbBuf)

	// Read entry
	entryBuf := make([]byte, entryLen)
	_, err = i.memory.ReadAt(entryBuf, int64(entryPtr))
	if err != nil {
		return XqdError
	}
	entry := string(entryBuf)

	// Get penalty box
	pb := i.getPenaltyBox(pbName)
	if pb == nil {
		i.abilog.Printf("penalty box '%s' not found", pbName)
		return XqdErrInvalidArgument
	}

	// Add to penalty box
	pb.Add(entry, ttl)

	return XqdStatusOK
}

// xqd_erl_penaltybox_has checks if entry is in the penalty box
// Returns 1 if present, 0 if not present
func (i *Instance) xqd_erl_penaltybox_has(
	pbPtr int32,
	pbLen int32,
	entryPtr int32,
	entryLen int32,
	hasEntryPtr int32, // out parameter
) int32 {
	i.abilog.Printf("fastly_erl::penaltybox_has")

	// Read penalty box name
	pbBuf := make([]byte, pbLen)
	_, err := i.memory.ReadAt(pbBuf, int64(pbPtr))
	if err != nil {
		return XqdError
	}
	pbName := string(pbBuf)

	// Read entry
	entryBuf := make([]byte, entryLen)
	_, err = i.memory.ReadAt(entryBuf, int64(entryPtr))
	if err != nil {
		return XqdError
	}
	entry := string(entryBuf)

	// Get penalty box
	pb := i.getPenaltyBox(pbName)
	if pb == nil {
		i.abilog.Printf("penalty box '%s' not found", pbName)
		return XqdErrInvalidArgument
	}

	// Check if in penalty box
	has := pb.Has(entry)

	// Write result (1 if present, 0 if not)
	var result uint32
	if has {
		result = 1
	} else {
		result = 0
	}
	i.memory.PutUint32(result, int64(hasEntryPtr))

	return XqdStatusOK
}

// getRateCounter retrieves a rate counter by name from the instance's registry.
// Returns nil if the rate counter is not found.
func (i *Instance) getRateCounter(name string) *RateCounter {
	for idx := range i.rateCounters {
		if i.rateCounters[idx].name == name {
			return i.rateCounters[idx].counter
		}
	}

	return nil
}

// getPenaltyBox retrieves a penalty box by name from the instance's registry.
// Returns nil if the penalty box is not found.
func (i *Instance) getPenaltyBox(name string) *PenaltyBox {
	for idx := range i.penaltyBoxes {
		if i.penaltyBoxes[idx].name == name {
			return i.penaltyBoxes[idx].box
		}
	}

	return nil
}

// addRateCounter registers a rate counter by name in the instance's registry.
// The registered counter can be accessed by guest programs through edge rate limiting APIs.
func (i *Instance) addRateCounter(name string, counter *RateCounter) {
	i.rateCounters = append(i.rateCounters, rateCounterEntry{
		name:    name,
		counter: counter,
	})
}

// addPenaltyBox registers a penalty box by name in the instance's registry.
// The registered penalty box can be accessed by guest programs through edge rate limiting APIs.
func (i *Instance) addPenaltyBox(name string, box *PenaltyBox) {
	i.penaltyBoxes = append(i.penaltyBoxes, penaltyBoxEntry{
		name: name,
		box:  box,
	})
}

// rateCounterEntry represents a named rate counter stored in the instance's registry.
// It associates a string name with a RateCounter implementation for edge rate limiting.
type rateCounterEntry struct {
	name    string
	counter *RateCounter
}

// penaltyBoxEntry represents a named penalty box stored in the instance's registry.
// It associates a string name with a PenaltyBox implementation for edge rate limiting.
type penaltyBoxEntry struct {
	name string
	box  *PenaltyBox
}
