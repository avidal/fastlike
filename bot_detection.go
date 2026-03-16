package fastlike

import "net/http"

// BotInfo contains information about bot detection for a request
type BotInfo struct {
	Analyzed     bool
	Detected     bool
	Name         string // e.g. "GoogleBot"
	Category     string // e.g. "SEARCH-ENGINE-CRAWLER"
	CategoryKind uint32 // enum discriminant
	Verified     *bool  // nil = not available
}

// BotDetectionFunc is a function that returns bot detection info for a request.
// Return nil for no bot detection data (equivalent to analyzed=false).
type BotDetectionFunc func(*http.Request) *BotInfo
