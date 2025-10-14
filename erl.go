package fastlike

import (
	"sync"
	"time"
)

// RateCounter tracks request counts over time windows for rate limiting.
// It uses a time-bucketed approach to efficiently calculate rates and counts
// over sliding windows.
type RateCounter struct {
	mu      sync.RWMutex
	entries map[string]*RateCounterEntry
}

// RateCounterEntry tracks the count history for a single entry (e.g., IP address).
// It stores timestamps and deltas to calculate rates over various time windows.
type RateCounterEntry struct {
	// events stores timestamp -> delta pairs
	events []CounterEvent
}

// CounterEvent represents a single count event with timestamp and delta
type CounterEvent struct {
	timestamp time.Time
	delta     uint32
}

// NewRateCounter creates a new rate counter
func NewRateCounter() *RateCounter {
	return &RateCounter{
		entries: make(map[string]*RateCounterEntry),
	}
}

// Increment adds delta to the counter for the given entry
func (rc *RateCounter) Increment(entry string, delta uint32) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	now := time.Now()

	if rc.entries[entry] == nil {
		rc.entries[entry] = &RateCounterEntry{
			events: []CounterEvent{},
		}
	}

	// Add the new event
	rc.entries[entry].events = append(rc.entries[entry].events, CounterEvent{
		timestamp: now,
		delta:     delta,
	})

	// Clean up old events (keep last 1 hour of data)
	rc.cleanupOldEvents(entry, now.Add(-time.Hour))
}

// LookupRate calculates the rate (requests per second) over the given window
// window is in seconds
func (rc *RateCounter) LookupRate(entry string, window uint32) uint32 {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	e := rc.entries[entry]
	if e == nil {
		return 0
	}

	now := time.Now()
	windowStart := now.Add(-time.Duration(window) * time.Second)

	count := rc.countEventsInWindow(e, windowStart, now)

	// Calculate rate per second
	if window == 0 {
		return 0
	}

	return count / window
}

// LookupCount returns the total count over the given duration (in seconds)
func (rc *RateCounter) LookupCount(entry string, duration uint32) uint32 {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	e := rc.entries[entry]
	if e == nil {
		return 0
	}

	now := time.Now()
	windowStart := now.Add(-time.Duration(duration) * time.Second)

	return rc.countEventsInWindow(e, windowStart, now)
}

// countEventsInWindow counts events between start and end times
func (rc *RateCounter) countEventsInWindow(entry *RateCounterEntry, start, end time.Time) uint32 {
	var count uint32

	for _, event := range entry.events {
		if event.timestamp.After(start) && event.timestamp.Before(end) || event.timestamp.Equal(start) || event.timestamp.Equal(end) {
			count += event.delta
		}
	}

	return count
}

// cleanupOldEvents removes events older than the cutoff time
func (rc *RateCounter) cleanupOldEvents(entry string, cutoff time.Time) {
	e := rc.entries[entry]
	if e == nil {
		return
	}

	// Filter out old events
	newEvents := make([]CounterEvent, 0, len(e.events))
	for _, event := range e.events {
		if event.timestamp.After(cutoff) {
			newEvents = append(newEvents, event)
		}
	}

	e.events = newEvents
}

// PenaltyBox tracks entries that have exceeded rate limits
type PenaltyBox struct {
	mu      sync.RWMutex
	entries map[string]time.Time // entry -> expiration time
}

// NewPenaltyBox creates a new penalty box
func NewPenaltyBox() *PenaltyBox {
	return &PenaltyBox{
		entries: make(map[string]time.Time),
	}
}

// Add adds an entry to the penalty box for the given TTL (in seconds)
// TTL is truncated to the nearest minute and must be between 1m and 1h
func (pb *PenaltyBox) Add(entry string, ttl uint32) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	// Clamp TTL to valid range (60s to 3600s) and round to nearest minute
	if ttl < 60 {
		ttl = 60
	} else if ttl > 3600 {
		ttl = 3600
	}

	// Truncate to nearest minute
	ttl = (ttl / 60) * 60

	expiration := time.Now().Add(time.Duration(ttl) * time.Second)
	pb.entries[entry] = expiration
}

// Has checks if an entry is in the penalty box (not expired)
func (pb *PenaltyBox) Has(entry string) bool {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	expiration, exists := pb.entries[entry]
	if !exists {
		return false
	}

	// Check if expired
	if time.Now().After(expiration) {
		// Entry has expired, but we'll clean it up lazily
		return false
	}

	return true
}

// Cleanup removes expired entries from the penalty box
// This is called periodically to prevent memory leaks
func (pb *PenaltyBox) Cleanup() {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	now := time.Now()
	for entry, expiration := range pb.entries {
		if now.After(expiration) {
			delete(pb.entries, entry)
		}
	}
}

// CheckRate is a convenience function that combines rate checking and penalty box logic.
// It increments the counter by delta, checks if the rate exceeds the limit over the window,
// and if so, adds the entry to the penalty box.
// Returns 1 if the entry should be blocked (rate exceeded), 0 otherwise.
func CheckRate(rc *RateCounter, pb *PenaltyBox, entry string, delta, window, limit uint32, ttl uint32) uint32 {
	// First check if already in penalty box
	if pb.Has(entry) {
		return 1 // blocked
	}

	// Increment the counter
	rc.Increment(entry, delta)

	// Check the rate over the window
	rate := rc.LookupRate(entry, window)

	// If rate exceeds limit, add to penalty box
	if rate > limit {
		pb.Add(entry, ttl)
		return 1 // blocked
	}

	return 0 // not blocked
}
