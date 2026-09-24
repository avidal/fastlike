package fastlike

import (
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// These helpers port production's cache-semantics crate, which implements
// RFC 9111.

// storedResponse is the response head of an HTTP cache object, with the
// times its age is computed from.
type storedResponse struct {
	status       int
	header       http.Header
	requestTime  time.Time
	responseTime time.Time
}

// newStoredResponse keeps the header of resp, which the caller must own.
func newStoredResponse(resp *http.Response, requestTime time.Time) *storedResponse {
	header := resp.Header
	if header == nil {
		header = http.Header{}
	}
	return &storedResponse{
		status:       resp.StatusCode,
		header:       header,
		requestTime:  requestTime,
		responseTime: time.Now(),
	}
}

// currentAge follows RFC 9111 section 4.2.3.
func (s *storedResponse) currentAge(now time.Time) time.Duration {
	correctedAgeValue := ageValue(s.header) + s.responseTime.Sub(s.requestTime)

	date := s.responseTime
	if parsed, ok := s.headerTime("Date"); ok {
		date = parsed
	}
	apparentAge := max(0, s.responseTime.Sub(date))

	return max(apparentAge, correctedAgeValue) + now.Sub(s.responseTime)
}

func (s *storedResponse) headerTime(name string) (time.Time, bool) {
	return headerDate(s.header, name, s.responseTime)
}

// ageValue reads the first member of the Age field, or 0 when it is invalid.
func ageValue(h http.Header) time.Duration {
	first, _, _ := strings.Cut(h.Get("Age"), ",")
	secs, _ := parseDeltaSeconds(first)
	return time.Duration(secs) * time.Second
}

// defaultHTTPCacheTTL is the freshness lifetime production suggests for
// responses that do not state one.
const defaultHTTPCacheTTL = time.Hour

// suggestedCacheOptions are the write options production derives from a
// backend response, with durations in nanoseconds.
type suggestedCacheOptions struct {
	maxAgeNs               uint64
	initialAgeNs           uint64
	staleWhileRevalidateNs uint64
	staleIfErrorNs         uint64
	varyRule               string
}

// suggestCacheOptions mirrors production's suggested_cache_options.
// Production passes the same instant as the request and response times, so
// the initial age is only the Age field, without any response delay.
func suggestCacheOptions(h http.Header, now time.Time) suggestedCacheOptions {
	cc, _ := parseResponseCacheControl(h)
	return suggestedCacheOptions{
		maxAgeNs:               freshnessLifetimeNs(h, cc, now),
		initialAgeNs:           uint64(ageValue(h)),
		staleWhileRevalidateNs: directiveNs(cc.staleWhileRevalidate),
		staleIfErrorNs:         directiveNs(cc.staleIfError),
		varyRule:               suggestedVaryRule(h),
	}
}

// freshnessLifetimeNs follows RFC 9111 section 4.2.1 for a shared cache.
// Like production, an unparsable Expires falls back to the default lifetime
// rather than meaning already expired, and the result saturates at the
// largest u64 instead of Go's 292-year limit on durations.
func freshnessLifetimeNs(h http.Header, cc responseCacheControl, now time.Time) uint64 {
	if cc.sMaxAge != nil {
		return directiveNs(cc.sMaxAge)
	}
	if cc.maxAge != nil {
		return directiveNs(cc.maxAge)
	}
	expires, ok := headerDate(h, "Expires", now)
	if !ok {
		return uint64(defaultHTTPCacheTTL)
	}
	date, ok := headerDate(h, "Date", now)
	if !ok {
		date = now
	}
	secs := expires.Unix() - date.Unix()
	switch {
	case secs <= 0:
		return 0
	case uint64(secs) > math.MaxUint64/uint64(time.Second):
		return math.MaxUint64
	}
	// Parsed dates are whole seconds, so only now, standing in for a missing
	// Date, can carry a fraction.
	return uint64(secs)*uint64(time.Second) - uint64(date.Nanosecond())
}

func directiveNs(secs *uint32) uint64 {
	if secs == nil {
		return 0
	}
	return uint64(*secs) * uint64(time.Second)
}

// suggestedVaryRule lists the fields that Vary nominates, lowercased, in
// order and with duplicates, skipping malformed Vary lines.
// Production keeps `*` as an ordinary name, which no request carries, so a
// `Vary: *` response ends up matching every request.
func suggestedVaryRule(h http.Header) string {
	var names []string
	for _, value := range h.Values("Vary") {
		if fields, ok := parseFieldNames(value); ok {
			for _, field := range fields {
				names = append(names, strings.ToLower(field))
			}
		}
	}
	return strings.Join(names, " ")
}

func headerDate(h http.Header, name string, now time.Time) (time.Time, bool) {
	value, ok := firstHeaderValue(h, name)
	if !ok {
		return time.Time{}, false
	}
	return parseHTTPDate(now, value)
}

var (
	httpDateShortDays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	httpDateLongDays  = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	httpDateMonths    = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
)

// parseHTTPDate ports production's HTTP-date parser, which is stricter than
// http.ParseTime about weekdays and resolves two-digit years relative to now.
// Names are matched without regard to case.
func parseHTTPDate(now time.Time, s string) (time.Time, bool) {
	if t, ok := parseIMFFixdate(&httpDateScanner{s: s}); ok {
		return t, true
	}
	if t, ok := parseRFC850Date(&httpDateScanner{s: s}, now); ok {
		return t, true
	}
	return parseAsctimeDate(&httpDateScanner{s: s})
}

// parseIMFFixdate reads "Sun, 06 Nov 1994 08:49:37 GMT".
func parseIMFFixdate(sc *httpDateScanner) (time.Time, bool) {
	wd := sc.name(httpDateShortDays)
	sc.literal(", ")
	day := sc.number(2)
	sc.literal(" ")
	month := sc.name(httpDateMonths)
	sc.literal(" ")
	year := sc.number(4)
	sc.literal(" ")
	clock := sc.clock()
	sc.literal(" gmt")
	return sc.date(wd, year, month, day, clock)
}

// parseRFC850Date reads "Sunday, 06-Nov-94 08:49:37 GMT".
func parseRFC850Date(sc *httpDateScanner, now time.Time) (time.Time, bool) {
	wd := sc.name(httpDateLongDays)
	sc.literal(", ")
	day := sc.number(2)
	sc.literal("-")
	month := sc.name(httpDateMonths)
	sc.literal("-")
	year := closestYear(now, sc.number(2))
	sc.literal(" ")
	clock := sc.clock()
	sc.literal(" gmt")
	return sc.date(wd, year, month, day, clock)
}

// parseAsctimeDate reads "Sun Nov  6 08:49:37 1994".
func parseAsctimeDate(sc *httpDateScanner) (time.Time, bool) {
	wd := sc.name(httpDateShortDays)
	sc.literal(" ")
	month := sc.name(httpDateMonths)
	sc.literal(" ")
	var day int
	if sc.optional(" ") {
		day = sc.number(1)
	} else {
		day = sc.number(2)
	}
	sc.literal(" ")
	clock := sc.clock()
	sc.literal(" ")
	year := sc.number(4)
	return sc.date(wd, year, month, day, clock)
}

// closestYear interprets a two-digit year as the closest year within 50
// years of now, per RFC 9110 section 5.6.7, as production does.
func closestYear(now time.Time, twoDigits int) int {
	current := now.UTC().Year()
	year := current/100*100 + twoDigits
	switch {
	case year-current > 50:
		return year - 100
	case year-current < -50:
		return year + 100
	}
	return year
}

// httpDateScanner matches fixed-width fields.
// Every field is followed by a separator or the end of the input, so this
// accepts what production's lexer and grammar accept.
// Once a match fails, every later one does too.
type httpDateScanner struct {
	s      string
	pos    int
	failed bool
}

func (sc *httpDateScanner) optional(lit string) bool {
	if sc.failed || len(sc.s)-sc.pos < len(lit) || !strings.EqualFold(sc.s[sc.pos:sc.pos+len(lit)], lit) {
		return false
	}
	sc.pos += len(lit)
	return true
}

func (sc *httpDateScanner) literal(lit string) {
	if !sc.optional(lit) {
		sc.failed = true
	}
}

func (sc *httpDateScanner) name(names []string) int {
	for idx, name := range names {
		if sc.optional(name) {
			return idx
		}
	}
	sc.failed = true
	return 0
}

func (sc *httpDateScanner) number(digits int) int {
	end := sc.pos + digits
	if sc.failed || end > len(sc.s) {
		sc.failed = true
		return 0
	}
	n := 0
	for _, c := range []byte(sc.s[sc.pos:end]) {
		if !isASCIIDigit(c) {
			sc.failed = true
			return 0
		}
		n = n*10 + int(c-'0')
	}
	sc.pos = end
	return n
}

func (sc *httpDateScanner) clock() [3]int {
	var clock [3]int
	clock[0] = sc.number(2)
	sc.literal(":")
	clock[1] = sc.number(2)
	sc.literal(":")
	clock[2] = sc.number(2)
	return clock
}

// date checks that the whole input matched, and validates the calendar date,
// the time of day and the weekday.
func (sc *httpDateScanner) date(wd, year, month, day int, clock [3]int) (time.Time, bool) {
	if sc.failed || sc.pos != len(sc.s) || clock[0] > 23 || clock[1] > 59 || clock[2] > 59 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month+1), day, clock[0], clock[1], clock[2], 0, time.UTC)
	if t.Year() != year || t.Month() != time.Month(month+1) || t.Day() != day || int(t.Weekday()) != wd {
		return time.Time{}, false
	}
	return t, true
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// ageHeaderValue formats an age as RFC 9111 delta-seconds, rounded up.
func ageHeaderValue(age time.Duration) string {
	secs := min(max(math.Ceil(age.Seconds()), 0), 2147483648)
	return strconv.FormatUint(uint64(secs), 10)
}

// parseDeltaSeconds saturates at the u32 maximum and, like production, reads
// an empty string as zero.
func parseDeltaSeconds(s string) (uint32, bool) {
	var secs uint64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		secs = min(secs*10+uint64(s[i]-'0'), math.MaxUint32)
	}
	return uint32(secs), true
}

// validatorsMatch reports whether the client's validators allow a 304.
// Like production, If-None-Match is split on commas without trimming.
func (s *storedResponse) validatorsMatch(req *http.Request) bool {
	if values := req.Header.Values("If-None-Match"); len(values) > 0 {
		if values[0] == "*" {
			return true
		}
		etag, ok := firstHeaderValue(s.header, "Etag")
		if !ok {
			return false
		}
		for _, value := range values {
			if slices.Contains(strings.Split(value, ","), etag) {
				return true
			}
		}
		return false
	}

	sinceTime, ok := headerDate(req.Header, "If-Modified-Since", time.Now())
	if !ok {
		return false
	}
	lastModified, ok := s.headerTime("Last-Modified")
	if !ok {
		if lastModified, ok = s.headerTime("Date"); !ok {
			lastModified = s.responseTime
		}
	}
	return lastModified.Unix() <= sinceTime.Unix()
}

// byteRange is an inclusive range of a cached body.
// Either bound can be missing, and without both it stands for the whole body.
type byteRange struct {
	first, last       uint64
	hasFirst, hasLast bool
}

// requestedRange mirrors production's handle_range_request.
// It only considers GET requests without If-Range, and only the first range
// of the first Range field: anything after it is ignored, and an overflowing
// bound counts as missing.
// A range whose last byte is not after its first one is not served, so a
// single-byte range gets the whole response.
func requestedRange(req *http.Request) (byteRange, bool) {
	if req.Method != http.MethodGet || len(req.Header.Values("If-Range")) > 0 {
		return byteRange{}, false
	}
	value, ok := firstHeaderValue(req.Header, "Range")
	if !ok || !isVisibleASCII(value) {
		return byteRange{}, false
	}
	spec, ok := strings.CutPrefix(value, "bytes=")
	if !ok {
		return byteRange{}, false
	}
	firstDigits := leadingDigits(spec)
	rest, ok := strings.CutPrefix(spec[len(firstDigits):], "-")
	if !ok {
		return byteRange{}, false
	}
	var r byteRange
	if first, err := strconv.ParseUint(firstDigits, 10, 64); err == nil {
		r.first, r.hasFirst = first, true
	}
	if last, err := strconv.ParseUint(leadingDigits(rest), 10, 64); err == nil {
		r.last, r.hasLast = last, true
	}
	switch {
	case r.hasFirst && r.hasLast:
		return r, r.first < r.last
	case r.hasLast:
		return r, r.last > 0
	}
	return r, r.hasFirst
}

// contentRange is what production puts in Content-Range: the requested
// bounds, never clamped, against the length known when the lookup happened.
// Prefix and suffix ranges of an object of unknown length get no 206.
// The arithmetic wraps like production's release build does, for instance
// with a suffix longer than the object.
func (r byteRange) contentRange(total int64, totalKnown bool) (string, bool) {
	if !totalKnown {
		if r.hasFirst && r.hasLast {
			return fmt.Sprintf("bytes %d-%d/*", r.first, r.last), true
		}
		return "", false
	}
	t := uint64(total)
	first, last := r.first, r.last
	switch {
	case !r.hasLast:
		last = t - 1
	case !r.hasFirst:
		first, last = t-r.last, t-1
	}
	return fmt.Sprintf("bytes %d-%d/%d", first, last, t), true
}

func leadingDigits(s string) string {
	n := 0
	for n < len(s) && isASCIIDigit(s[n]) {
		n++
	}
	return s[:n]
}

// isVisibleASCII matches the http crate's HeaderValue::to_str.
func isVisibleASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c != '\t' && (c < ' ' || c > '~') {
			return false
		}
	}
	return true
}

// notModifiedHeader keeps the fields a 304 needs, plus Last-Modified, which
// production always adds.
func notModifiedHeader(stored http.Header) http.Header {
	header := http.Header{}
	for _, name := range []string{"Cache-Control", "Content-Location", "Date", "Etag", "Expires", "Vary"} {
		if values := stored.Values(name); len(values) > 0 {
			header[name] = slices.Clone(values)
		}
	}
	if lastModified, ok := firstHeaderValue(stored, "Last-Modified"); ok {
		header.Set("Last-Modified", lastModified)
	}
	return header
}

// prepareRevalidationRequest makes a lookup request conditional on the stored
// response.
func prepareRevalidationRequest(r *http.Request, stored http.Header) {
	if etag, ok := firstHeaderValue(stored, "Etag"); ok {
		r.Header.Set("If-None-Match", etag)
	}
	if lastModified, ok := firstHeaderValue(stored, "Last-Modified"); ok {
		r.Header.Set("If-Modified-Since", lastModified)
	}
	if r.Method == http.MethodHead {
		r.Method = http.MethodGet
	}
	r.Header.Del("Range")
}

// freshenStoredHeader applies a 304 to a stored head, per RFC 9111 section 3.2.
// Like production, the stored directives decide which fields are stripped.
func freshenStoredHeader(stored, notModified http.Header) http.Header {
	merged := stored.Clone()
	for name, values := range notModified {
		if key := http.CanonicalHeaderKey(name); key != "Content-Length" {
			merged[key] = slices.Clone(values)
		}
	}
	stripHeadersForStorage(merged, stored)
	return merged
}

// stripHeadersForStorage removes the fields a shared cache must not store,
// following the cache directives found in directives.
func stripHeadersForStorage(h, directives http.Header) {
	names := []string{
		"Proxy-Connection", "Keep-Alive", "Te", "Transfer-Encoding", "Upgrade",
		"Proxy-Authenticate", "Proxy-Authentication-Info", "Proxy-Authorization", "Connection",
	}
	for _, value := range h.Values("Connection") {
		if fields, ok := parseFieldNames(value); ok {
			names = append(names, fields...)
		}
	}
	if cc, ok := parseResponseCacheControl(directives); ok {
		names = append(names, cc.noCache.fields...)
		names = append(names, cc.private.fields...)
	}
	for _, name := range names {
		h.Del(name)
	}
}

// maybeQualified is a directive like no-cache that may list field names.
type maybeQualified struct {
	present bool
	fields  []string
}

// set keeps the first well-formed occurrence, as production does.
func (q *maybeQualified) set(d cacheDirective) {
	if q.present {
		return
	}
	if !d.hasArg {
		q.present = true
		return
	}
	if d.arg == "" {
		return
	}
	if fields, ok := parseFieldNames(d.arg); ok {
		q.present = true
		q.fields = fields
	}
}

func (q maybeQualified) unqualified() bool {
	return q.present && q.fields == nil
}

// responseCacheControl holds the response directives the HTTP cache uses.
type responseCacheControl struct {
	noCache        maybeQualified
	private        maybeQualified
	noStore        bool
	mustUnderstand bool
	public         bool
	maxAge         *uint32
	sMaxAge        *uint32

	staleWhileRevalidate *uint32
	staleIfError         *uint32
}

// parseResponseCacheControl reads Surrogate-Control, or Cache-Control when it
// is absent, and fails on a malformed value.
func parseResponseCacheControl(h http.Header) (responseCacheControl, bool) {
	var cc responseCacheControl
	values := h.Values("Surrogate-Control")
	if len(values) == 0 {
		values = h.Values("Cache-Control")
	}
	for _, value := range values {
		directives, ok := parseCacheDirectives(value)
		if !ok {
			return responseCacheControl{}, false
		}
		for _, d := range directives {
			switch d.name {
			case "no-cache":
				cc.noCache.set(d)
			case "private":
				cc.private.set(d)
			case "no-store":
				cc.noStore = cc.noStore || !d.hasArg
			case "must-understand":
				cc.mustUnderstand = cc.mustUnderstand || !d.hasArg
			case "public":
				cc.public = cc.public || !d.hasArg
			case "max-age":
				setDirectiveSeconds(&cc.maxAge, d)
			case "s-maxage":
				setDirectiveSeconds(&cc.sMaxAge, d)
			case "stale-while-revalidate":
				setDirectiveSeconds(&cc.staleWhileRevalidate, d)
			case "stale-if-error":
				setDirectiveSeconds(&cc.staleIfError, d)
			}
		}
	}
	return cc, true
}

func setDirectiveSeconds(field **uint32, d cacheDirective) {
	if *field != nil || !d.hasArg {
		return
	}
	if secs, ok := parseDeltaSeconds(d.arg); ok {
		*field = &secs
	}
}

// storageActionFor decides whether a response gets stored, applying RFC 9111
// section 3 with production's settings.
func storageActionFor(method string, resp *http.Response) uint32 {
	cc, ccOK := parseResponseCacheControl(resp.Header)
	methodAndStatus := hasCacheableMethodAndStatus(method, resp.StatusCode, cc)

	prevented := !ccOK || (cc.noStore && !cc.mustUnderstand) || cc.private.unqualified()
	_, hasExpires := resp.Header["Expires"]
	intent := ccOK && (cc.public || hasExpires || cc.maxAge != nil || cc.sMaxAge != nil ||
		heuristicallyCacheableStatus(resp.StatusCode))
	if methodAndStatus && !prevented && intent {
		return HttpStorageActionInsert
	}

	if methodAndStatus && ccOK && (heuristicallyCacheableStatus(resp.StatusCode) || cc.public) {
		return HttpStorageActionRecordUncacheable
	}
	return HttpStorageActionDoNotStore
}

func hasCacheableMethodAndStatus(method string, status int, cc responseCacheControl) bool {
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	if status >= 100 && status < 200 {
		return false
	}
	if status == http.StatusPartialContent || status == http.StatusNotModified {
		return false
	}
	if cc.mustUnderstand {
		return heuristicallyCacheableStatus(status)
	}
	return true
}

// heuristicallyCacheableStatus follows RFC 9110 section 15.1.
func heuristicallyCacheableStatus(status int) bool {
	switch status {
	case 200, 203, 204, 206, 300, 301, 308, 404, 405, 410, 414, 501:
		return true
	}
	return false
}

type cacheDirective struct {
	name   string
	arg    string
	hasArg bool
}

// parseCacheDirectives parses one Cache-Control value with production's
// grammar.
func parseCacheDirectives(value string) ([]cacheDirective, bool) {
	var directives []cacheDirective
	pos := 0
	for {
		pos = skipListSeparators(value, pos)
		if pos == len(value) {
			break
		}
		name := scanToken(value[pos:])
		if name == "" {
			return nil, false
		}
		pos += len(name)
		d := cacheDirective{name: strings.ToLower(name)}
		pos = skipWhitespace(value, pos)
		if pos < len(value) && value[pos] == '=' {
			pos = skipWhitespace(value, pos+1)
			arg, n, ok := scanDirectiveArg(value[pos:])
			if !ok {
				return nil, false
			}
			d.arg, d.hasArg = arg, true
			pos = skipWhitespace(value, pos+n)
		}
		directives = append(directives, d)
		if pos < len(value) && value[pos] != ',' {
			return nil, false
		}
	}
	return directives, len(directives) > 0
}

// scanDirectiveArg reads a token or quoted string and returns its length.
// Like production, quoted strings lose every backslash.
func scanDirectiveArg(s string) (string, int, bool) {
	if token := scanToken(s); token != "" {
		return token, len(token), !isCacheControlKeyword(token)
	}
	if s == "" || s[0] != '"' {
		return "", 0, false
	}
	var arg strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			return arg.String(), i + 1, true
		case c == '\\':
			i++
			if i == len(s) || !isFieldValueChar(s[i]) {
				return "", 0, false
			}
			if s[i] != '\\' {
				arg.WriteByte(s[i])
			}
		case isFieldValueChar(c):
			arg.WriteByte(c)
		default:
			return "", 0, false
		}
	}
	return "", 0, false
}

// maxHeaderNameLen is the longest header name the http crate accepts.
const maxHeaderNameLen = 1<<16 - 1

// cacheControlKeywords are the directive names production's lexer gives
// their own tokens.
// Where its grammar expects a token, one of these names is a syntax error, so
// it can be neither a field name nor an unquoted directive argument.
var cacheControlKeywords = []string{
	"max-age", "max-stale", "min-fresh", "no-cache", "no-store", "no-transform", "only-if-cached",
	"must-revalidate", "must-understand", "private", "proxy-revalidate", "public", "s-maxage",
	"stale-if-error", "stale-while-revalidate",
}

func isCacheControlKeyword(token string) bool {
	return slices.ContainsFunc(cacheControlKeywords, func(keyword string) bool {
		return strings.EqualFold(keyword, token)
	})
}

// parseFieldNames parses the field name lists of Connection, Vary and
// qualified cache directives.
func parseFieldNames(value string) ([]string, bool) {
	var names []string
	pos := 0
	for {
		pos = skipListSeparators(value, pos)
		if pos == len(value) {
			break
		}
		name := scanToken(value[pos:])
		if name == "" || len(name) > maxHeaderNameLen || isCacheControlKeyword(name) {
			return nil, false
		}
		names = append(names, name)
		pos += len(name)
		if pos < len(value) && value[pos] != ',' {
			return nil, false
		}
	}
	return names, len(names) > 0
}

// skipListSeparators skips the commas between list elements and the
// whitespace allowed after each of them.
func skipListSeparators(s string, pos int) int {
	for pos < len(s) && s[pos] == ',' {
		pos = skipWhitespace(s, pos+1)
	}
	return pos
}

func skipWhitespace(s string, pos int) int {
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
		pos++
	}
	return pos
}

func scanToken(s string) string {
	n := 0
	for n < len(s) && isTokenChar(s[n]) {
		n++
	}
	return s[:n]
}

// firstHeaderValue distinguishes an empty value from a missing header.
func firstHeaderValue(h http.Header, name string) (string, bool) {
	values := h.Values(name)
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}
