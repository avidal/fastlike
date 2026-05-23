package fastlike

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed profile_ui_assets/timeline.js
var timelineJS []byte

// profileUITimelineAssetPath is the single asset URL the embedded canvas
// viewer is served from. Exported as a constant so tests and the template
// reference one source of truth.
const profileUITimelineAssetPath = "/assets/timeline.js"

// ProfileUI is the read-only HTTP handler the operator points a browser at
// to inspect captured traces. It serves three routes:
//
//   - GET /                     index of recent traces (newest first)
//   - GET /r/{req_id}           per-request page with summary, span table,
//     and a server-rendered backend waterfall
//   - GET /r/{req_id}.json      the raw native JSON trace
//
// The handler is intentionally allocation-light and template-driven so it
// can render without any client-side JavaScript. Live-tail (SSE) and the
// flame/waterfall canvas land in a later step. Auth and listener binding
// are the caller's responsibility (the CLI gates loopback vs non-loopback
// per the Security section of the plan); embedders are free to mount this
// handler behind their own middleware.
type ProfileUI struct {
	store *ProfileStore
}

// NewProfileUI returns a handler bound to store. The store may be nil, in
// which case every route returns 503; this lets the CLI build the
// handler unconditionally and rely on the listener gate to suppress
// exposure when profiling is off.
func NewProfileUI(store *ProfileStore) *ProfileUI {
	return &ProfileUI{store: store}
}

// ServeHTTP implements http.Handler.
func (p *ProfileUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.store == nil {
		http.Error(w, "profile store unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch {
	case r.URL.Path == "/" || r.URL.Path == "":
		p.serveIndex(w, r)
	case strings.HasPrefix(r.URL.Path, "/r/"):
		p.serveRequest(w, r)
	case r.URL.Path == profileUITimelineAssetPath:
		p.serveTimelineAsset(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (p *ProfileUI) serveTimelineAsset(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// Embedded asset never changes during a process's lifetime; the trace
	// data does, so we only allow short caching to keep behavior obvious
	// across reloads.
	w.Header().Set("Cache-Control", "public, max-age=60")
	_, _ = w.Write(timelineJS)
}

func (p *ProfileUI) serveIndex(w http.ResponseWriter, _ *http.Request) {
	traces := p.store.Recent(0)
	data := indexData{
		Title:    "fastlike profiler",
		Retain:   p.store.Retain(),
		Traces:   make([]indexRow, len(traces)),
		InFlight: len(p.store.InFlight()),
	}
	for i, t := range traces {
		data.Traces[i] = indexRow{
			ReqID:               t.ReqID,
			Method:              t.Method,
			URL:                 t.URL,
			Status:              t.Status,
			Outcome:             t.Outcome.String(),
			WallMillis:          fmt.Sprintf("%.2f", float64(t.WallNanos)/float64(time.Millisecond)),
			HostcallMillis:      fmt.Sprintf("%.2f", float64(t.HostcallNanos)/float64(time.Millisecond)),
			Spans:               len(t.Spans),
			BackendCalls:        len(t.BackendCalls),
			Dropped:             t.Dropped,
			DroppedBackendCalls: t.DroppedBackendCalls,
			ModuleID:            t.ModuleID,
		}
	}
	var buf bytes.Buffer
	if err := indexTmpl.Execute(&buf, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (p *ProfileUI) serveRequest(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/r/")
	if rest == "" || strings.Contains(rest, "/") {
		http.NotFound(w, r)
		return
	}
	idStr, suffix := splitRequestSuffix(rest)
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	trace := p.store.Get(id)
	if trace == nil {
		http.NotFound(w, r)
		return
	}

	handler, ok := requestDownloadHandlers[suffix]
	if !ok {
		http.NotFound(w, r)
		return
	}
	handler(p, w, trace)
}

// splitRequestSuffix returns (idStr, suffix) such that idStr+suffix == rest.
// Suffixes are matched longest-first so ".chrome.json" wins over ".json".
// Empty suffix means the bare HTML view.
func splitRequestSuffix(rest string) (string, string) {
	for _, suffix := range requestDownloadSuffixes {
		if strings.HasSuffix(rest, suffix) {
			return strings.TrimSuffix(rest, suffix), suffix
		}
	}
	return rest, ""
}

// requestDownloadHandlers maps a URL suffix to the function that
// renders the named export. The empty suffix is the HTML view;
// ".json" is the native trace schema; encoder-specific exports follow
// the same suffix-driven dispatch so adding firefox/pprof is one line
// here plus the encoder + content type.
var requestDownloadHandlers = map[string]func(*ProfileUI, http.ResponseWriter, *RequestTrace){
	"":              (*ProfileUI).serveRequestHTML,
	".json":         (*ProfileUI).serveRequestNativeJSON,
	".chrome.json":  (*ProfileUI).serveRequestChromeJSON,
	".firefox.json": (*ProfileUI).serveRequestFirefoxJSON,
	".pprof":        (*ProfileUI).serveRequestPprof,
}

// requestDownloadSuffixes is the longest-first match order for
// splitRequestSuffix. Keep this in sync with the map keys above; a
// future test could enforce the invariant if drift becomes a hazard.
var requestDownloadSuffixes = []string{
	".chrome.json",
	".firefox.json",
	".pprof",
	".json",
	"",
}

func (p *ProfileUI) serveRequestHTML(w http.ResponseWriter, trace *RequestTrace) {
	data := newRequestData(trace)
	var buf bytes.Buffer
	if err := requestTmpl.Execute(&buf, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (p *ProfileUI) serveRequestNativeJSON(w http.ResponseWriter, trace *RequestTrace) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(trace); err != nil {
		http.Error(w, "encode trace: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (p *ProfileUI) serveRequestChromeJSON(w http.ResponseWriter, trace *RequestTrace) {
	raw, err := EncodeChromeTrace(trace)
	if err != nil {
		http.Error(w, "chrome encode: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"fastlike-req-"+strconv.FormatUint(trace.ReqID, 10)+".chrome.json\"")
	_, _ = w.Write(raw)
}

func (p *ProfileUI) serveRequestFirefoxJSON(w http.ResponseWriter, trace *RequestTrace) {
	raw, err := EncodeFirefoxGecko(trace)
	if err != nil {
		http.Error(w, "firefox encode: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"fastlike-req-"+strconv.FormatUint(trace.ReqID, 10)+".firefox.json\"")
	_, _ = w.Write(raw)
}

func (p *ProfileUI) serveRequestPprof(w http.ResponseWriter, trace *RequestTrace) {
	raw, err := EncodePprof(trace)
	if err != nil {
		http.Error(w, "pprof encode: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// google/pprof writes gzip-compressed profile.proto bytes. The
	// canonical content-type for pprof downloads is application/octet-stream
	// with Content-Encoding: gzip; pprof tools detect the format from
	// the bytes themselves, not the content type.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"fastlike-req-"+strconv.FormatUint(trace.ReqID, 10)+".pprof\"")
	_, _ = w.Write(raw)
}

type indexData struct {
	Title    string
	Retain   int
	Traces   []indexRow
	InFlight int
}

type indexRow struct {
	ReqID               uint64
	Method              string
	URL                 string
	Status              int
	Outcome             string
	WallMillis          string
	HostcallMillis      string
	Spans               int
	BackendCalls        int
	Dropped             int
	DroppedBackendCalls int
	ModuleID            string
}

type requestData struct {
	ReqID            uint64
	ModuleID         string
	Method           string
	URL              string
	Status           int
	Outcome          string
	WallMillis       string
	GuestActiveMs    string
	HostcallMillis   string
	HeaderFlushMs    string
	HijackedMs       string
	Dropped          int
	DroppedBackends  int
	Spans            []requestSpan
	BackendWaterfall []backendBar
	NativeSamples    []nativeSampleRow
	Deep             *deepRow
	Notes            []string

	// JSONURL is the same-origin path the canvas viewer fetches its
	// data from. Stored on the data struct so a future reverse-proxy
	// mount under a sub-path (or unix-socket bind) can override it
	// without editing the template.
	JSONURL string

	// TimelineScriptPath points to the embedded viewer asset.
	TimelineScriptPath string

	// Downloads lists the third-party export URLs the page exposes.
	// Today: native JSON plus the Chrome Tracing export. Firefox Gecko
	// and pprof land in follow-on commits and add entries here.
	Downloads []downloadLink
}

type downloadLink struct {
	Label string
	URL   string
}

type nativeSampleRow struct {
	Idx      int
	StartMs  string
	Function string
}

type deepRow struct {
	BodyReadBytes      int64
	BodyWriteBytes     int64
	CacheLookups       int
	CacheInserts       int
	CacheHits          int
	CacheMisses        int
	CacheStale         int
	RequestHeaders     []HeaderSummary
	ResponseHeaders    []HeaderSummary
	HeapSamples        []HeapSample
	HeapSamplesDropped int
	HeapMin            int64
	HeapMax            int64
	HeapFinal          int64
	StoreAccess        []StoreAccess
}

type requestSpan struct {
	Idx        int
	Name       string
	StartMs    string
	DurationMs string
	RC         int32
	Tags       string
}

type backendBar struct {
	Idx         int
	Name        string
	Method      string
	URL         string
	Status      int
	Outcome     string
	XPct        float64 // percent across the waterfall canvas
	WidthPct    float64
	TotalMillis string
	StartedMs   string
	DNSMs       string
	ConnectMs   string
	TLSMs       string
	TTFBMs      string
	HasPhases   bool
}

func newRequestData(t *RequestTrace) requestData {
	data := requestData{
		ReqID:              t.ReqID,
		ModuleID:           t.ModuleID,
		Method:             t.Method,
		URL:                t.URL,
		Status:             t.Status,
		Outcome:            t.Outcome.String(),
		WallMillis:         fmtMs(t.WallNanos),
		GuestActiveMs:      fmtMs(t.GuestActiveNanos),
		HostcallMillis:     fmtMs(t.HostcallNanos),
		HeaderFlushMs:      fmtOptMs(t.HeaderFlushNanos),
		HijackedMs:         fmtOptMs(t.HijackedNanos),
		Dropped:            t.Dropped,
		DroppedBackends:    t.DroppedBackendCalls,
		Notes:              append([]string(nil), t.Notes...),
		JSONURL:            fmt.Sprintf("/r/%d.json", t.ReqID),
		TimelineScriptPath: profileUITimelineAssetPath,
		Deep:               deepRowOf(t),
		Downloads: []downloadLink{
			{Label: "native JSON", URL: fmt.Sprintf("/r/%d.json", t.ReqID)},
			{Label: "Chrome / Perfetto", URL: fmt.Sprintf("/r/%d.chrome.json", t.ReqID)},
			{Label: "Firefox profiler", URL: fmt.Sprintf("/r/%d.firefox.json", t.ReqID)},
			{Label: "pprof", URL: fmt.Sprintf("/r/%d.pprof", t.ReqID)},
		},
	}
	for i, s := range t.Spans {
		data.Spans = append(data.Spans, requestSpan{
			Idx:        i,
			Name:       resolveHostcallName(s.NameIdx),
			StartMs:    fmtMs(s.Start),
			DurationMs: fmtMs(s.Duration),
			RC:         s.RC,
			Tags:       fmt.Sprintf("%v", s.TagSlots),
		})
	}
	total := float64(t.WallNanos)
	if total <= 0 {
		// Avoid div-by-zero; fall back to the largest backend call total.
		for _, c := range t.BackendCalls {
			if float64(c.Started+c.TotalNanos) > total {
				total = float64(c.Started + c.TotalNanos)
			}
		}
		if total <= 0 {
			total = 1
		}
	}
	for i, s := range t.SnapshotNativeSamples() {
		data.NativeSamples = append(data.NativeSamples, nativeSampleRow{
			Idx:      i,
			StartMs:  fmtMs(s.RelativeNanos),
			Function: s.Function,
		})
	}
	for i, c := range t.BackendCalls {
		x := float64(c.Started) / total * 100
		width := float64(c.TotalNanos) / total * 100
		if width < 0.25 {
			width = 0.25 // keep a visible sliver for fast calls
		}
		data.BackendWaterfall = append(data.BackendWaterfall, backendBar{
			Idx:         i,
			Name:        c.Name,
			Method:      c.Method,
			URL:         c.URLRedacted,
			Status:      c.Status,
			Outcome:     c.Outcome.String(),
			XPct:        x,
			WidthPct:    width,
			TotalMillis: fmtMs(c.TotalNanos),
			StartedMs:   fmtMs(c.Started),
			DNSMs:       fmtOptMs(c.DNSNanos),
			ConnectMs:   fmtOptMs(c.ConnectNanos),
			TLSMs:       fmtOptMs(c.TLSNanos),
			TTFBMs:      fmtOptMs(c.TTFBNanos),
			HasPhases:   c.DNSNanos != nil || c.ConnectNanos != nil || c.TLSNanos != nil || c.TTFBNanos != nil,
		})
	}
	return data
}

// deepRowOf returns nil when the trace did not carry DeepMetrics so the
// template's `{{if .Deep}}` block short-circuits.
func deepRowOf(t *RequestTrace) *deepRow {
	if t.Deep == nil {
		return nil
	}
	hagg := HeapAggregatesOf(t.Deep.HeapSamples)
	return &deepRow{
		BodyReadBytes:      t.Deep.BodyReadBytes,
		BodyWriteBytes:     t.Deep.BodyWriteBytes,
		CacheLookups:       t.Deep.CacheLookups,
		CacheInserts:       t.Deep.CacheInserts,
		CacheHits:          t.Deep.CacheHits,
		CacheMisses:        t.Deep.CacheMisses,
		CacheStale:         t.Deep.CacheStale,
		RequestHeaders:     t.Deep.RequestHeaders,
		ResponseHeaders:    t.Deep.ResponseHeaders,
		HeapSamples:        t.Deep.HeapSamples,
		HeapSamplesDropped: t.Deep.HeapSamplesDropped,
		HeapMin:            hagg.Min,
		HeapMax:            hagg.Max,
		HeapFinal:          hagg.Final,
		StoreAccess:        t.Deep.StoreAccess,
	}
}

func fmtMs(nanos int64) string {
	return fmt.Sprintf("%.3f", float64(nanos)/float64(time.Millisecond))
}

func fmtOptMs(nanos *int64) string {
	if nanos == nil {
		return ""
	}
	return fmtMs(*nanos)
}

var indexTmpl = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Title}}</title>
<style>
body { font: 14px/1.4 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 2rem auto; max-width: 1100px; color: #1d1d1f; }
table { border-collapse: collapse; width: 100%; margin-top: 1rem; }
th, td { text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid #e3e3e3; }
th { background: #f6f6f6; font-weight: 600; }
tr:hover td { background: #fafaff; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.muted { color: #888; }
.bad { color: #b00020; }
.dropped { color: #c75800; }
.empty { padding: 1rem; background: #f9f9f9; border: 1px solid #e3e3e3; border-radius: 4px; color: #666; }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
<p class="muted">retain={{.Retain}} &middot; in-flight={{.InFlight}} &middot; total={{len .Traces}}</p>
{{if .Traces}}
<table>
<thead><tr>
  <th>req</th><th>method</th><th>url</th><th>status</th>
  <th>outcome</th><th>wall (ms)</th><th>hostcall (ms)</th>
  <th>spans</th><th>backends</th><th>drops</th><th>module</th>
</tr></thead>
<tbody>
{{range .Traces}}
<tr>
  <td><a href="/r/{{.ReqID}}"><code>{{.ReqID}}</code></a></td>
  <td><code>{{.Method}}</code></td>
  <td><code>{{.URL}}</code></td>
  <td>{{.Status}}</td>
  <td>{{if ne .Outcome "normal"}}<span class="bad">{{.Outcome}}</span>{{else}}{{.Outcome}}{{end}}</td>
  <td>{{.WallMillis}}</td>
  <td>{{.HostcallMillis}}</td>
  <td>{{.Spans}}{{if gt .Dropped 0}} <span class="dropped">+{{.Dropped}} dropped</span>{{end}}</td>
  <td>{{.BackendCalls}}{{if gt .DroppedBackendCalls 0}} <span class="dropped">+{{.DroppedBackendCalls}} dropped</span>{{end}}</td>
  <td>{{if or (gt .Dropped 0) (gt .DroppedBackendCalls 0)}}<span class="dropped">!</span>{{else}}&middot;{{end}}</td>
  <td><code class="muted">{{.ModuleID}}</code></td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<div class="empty">No completed traces yet. Send a request through fastlike, then refresh.</div>
{{end}}
</body>
</html>
`))

var requestTmpl = template.Must(template.New("request").Funcs(template.FuncMap{
	"mulRem": func(idx int) float64 { return float64(idx) },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>request {{.ReqID}}</title>
<style>
body { font: 14px/1.4 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 2rem auto; max-width: 1100px; color: #1d1d1f; }
h1, h2 { margin-top: 1.6rem; }
table { border-collapse: collapse; width: 100%; margin-top: 0.75rem; }
th, td { text-align: left; padding: 0.35rem 0.6rem; border-bottom: 1px solid #e3e3e3; vertical-align: top; }
th { background: #f6f6f6; font-weight: 600; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.muted { color: #888; }
.bad { color: #b00020; }
.dropped { color: #c75800; }
.waterfall { position: relative; height: {{len .BackendWaterfall | printf "%d" }}rem; background: #fafafa; border: 1px solid #e3e3e3; margin: 0.5rem 0; min-height: 1rem; }
.bar { position: absolute; height: 0.85rem; background: #4a7dff; border-radius: 2px; }
.bar.synthetic { background: #c75800; }
.bar.error { background: #b00020; }
.bar.orphan { background: #888; }
.bar.incomplete { background: #444; }
.summary td:first-child { width: 14rem; color: #444; }
.notes { background: #fff7eb; border: 1px solid #ffd7a8; padding: 0.5rem 0.75rem; border-radius: 4px; margin: 1rem 0; }
</style>
</head>
<body>
<p><a href="/">&larr; back to index</a></p>
<h1>request {{.ReqID}}</h1>
<p>
  <code>{{.Method}} {{.URL}}</code>
  &rarr; <strong>{{.Status}}</strong>
  &middot; outcome=<strong>{{if ne .Outcome "normal"}}<span class="bad">{{.Outcome}}</span>{{else}}{{.Outcome}}{{end}}</strong>
</p>
<p>downloads:
{{range $i, $d := .Downloads}}{{if $i}} &middot; {{end}}<a href="{{$d.URL}}">{{$d.Label}}</a>{{end}}
</p>

<h2>summary</h2>
<table class="summary">
<tr><td>module</td><td><code>{{.ModuleID}}</code></td></tr>
<tr><td>wall (ms)</td><td>{{.WallMillis}}</td></tr>
<tr><td>guest active (ms)</td><td>{{.GuestActiveMs}}</td></tr>
<tr><td>hostcall (ms)</td><td>{{.HostcallMillis}}</td></tr>
{{if .HeaderFlushMs}}<tr><td>header flush (ms)</td><td>{{.HeaderFlushMs}}</td></tr>{{end}}
{{if .HijackedMs}}<tr><td>hijacked (ms)</td><td>{{.HijackedMs}}</td></tr>{{end}}
{{if or (gt .Dropped 0) (gt .DroppedBackends 0)}}
<tr><td>dropped</td><td><span class="dropped">spans={{.Dropped}} backend_calls={{.DroppedBackends}}</span></td></tr>
{{end}}
</table>

{{if .Notes}}
<div class="notes">
<strong>notes</strong>
<ul>{{range .Notes}}<li>{{.}}</li>{{end}}</ul>
</div>
{{end}}

<h2>timeline</h2>
<noscript><p class="muted">canvas viewer requires JavaScript; the tables below are the no-JS view.</p></noscript>
<div id="fl-timeline" data-json-url="{{.JSONURL}}"></div>
<script src="{{.TimelineScriptPath}}" defer></script>

<h2>backend waterfall ({{len .BackendWaterfall}})</h2>
{{if .BackendWaterfall}}
<div class="waterfall">
{{range .BackendWaterfall}}
<div class="bar {{if eq .Outcome "synthetic-failure"}}synthetic{{else if eq .Outcome "network-error"}}error{{else if eq .Outcome "orphaned"}}orphan{{else if eq .Outcome "incomplete"}}incomplete{{end}}"
     style="left: {{printf "%.3f" .XPct}}%; width: {{printf "%.3f" .WidthPct}}%; top: {{printf "%.2frem" (.Idx | mulRem)}};"
     title="{{.Method}} {{.URL}} — {{.Outcome}} ({{.TotalMillis}}ms)"></div>
{{end}}
</div>
<table>
<thead><tr>
  <th>#</th><th>backend</th><th>method</th><th>url</th><th>status</th>
  <th>outcome</th><th>started (ms)</th><th>total (ms)</th>
  <th>dns</th><th>connect</th><th>tls</th><th>ttfb</th>
</tr></thead>
<tbody>
{{range .BackendWaterfall}}
<tr>
  <td>{{.Idx}}</td>
  <td><code>{{.Name}}</code></td>
  <td><code>{{.Method}}</code></td>
  <td><code>{{.URL}}</code></td>
  <td>{{.Status}}</td>
  <td>{{.Outcome}}</td>
  <td>{{.StartedMs}}</td>
  <td>{{.TotalMillis}}</td>
  <td>{{if .DNSMs}}{{.DNSMs}}{{else}}<span class="muted">&middot;</span>{{end}}</td>
  <td>{{if .ConnectMs}}{{.ConnectMs}}{{else}}<span class="muted">&middot;</span>{{end}}</td>
  <td>{{if .TLSMs}}{{.TLSMs}}{{else}}<span class="muted">&middot;</span>{{end}}</td>
  <td>{{if .TTFBMs}}{{.TTFBMs}}{{else}}<span class="muted">&middot;</span>{{end}}</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<p class="muted">no backend calls</p>
{{end}}

{{if .Deep}}
<h2>deep metrics</h2>
<table class="summary">
<tr><td>body bytes (read)</td><td>{{.Deep.BodyReadBytes}}</td></tr>
<tr><td>body bytes (write)</td><td>{{.Deep.BodyWriteBytes}}</td></tr>
<tr><td>cache lookups</td><td>{{.Deep.CacheLookups}}</td></tr>
<tr><td>cache inserts</td><td>{{.Deep.CacheInserts}}</td></tr>
<tr><td>cache hits</td><td>{{.Deep.CacheHits}}</td></tr>
<tr><td>cache misses</td><td>{{.Deep.CacheMisses}}</td></tr>
<tr><td>cache stale</td><td>{{.Deep.CacheStale}}</td></tr>
</table>
{{if .Deep.StoreAccess}}
<table>
<thead><tr><th>store kind</th><th>name</th><th>accesses</th></tr></thead>
<tbody>
{{range .Deep.StoreAccess}}
<tr>
  <td><code>{{.Kind}}</code></td>
  <td><code>{{.Name}}</code></td>
  <td>{{.Count}}</td>
</tr>
{{end}}
</tbody>
</table>
{{end}}

{{if .Deep.RequestHeaders}}
<h3>request headers ({{len .Deep.RequestHeaders}})</h3>
<table>
<thead><tr><th>name</th><th>count</th><th>bytes</th></tr></thead>
<tbody>
{{range .Deep.RequestHeaders}}
<tr>
  <td><code>{{.Name}}</code></td>
  <td>{{.Count}}</td>
  <td>{{.Bytes}}</td>
</tr>
{{end}}
</tbody>
</table>
{{end}}

{{if .Deep.ResponseHeaders}}
<h3>response headers ({{len .Deep.ResponseHeaders}})</h3>
<table>
<thead><tr><th>name</th><th>count</th><th>bytes</th></tr></thead>
<tbody>
{{range .Deep.ResponseHeaders}}
<tr>
  <td><code>{{.Name}}</code></td>
  <td>{{.Count}}</td>
  <td>{{.Bytes}}</td>
</tr>
{{end}}
</tbody>
</table>
{{end}}

{{if .Deep.HeapSamples}}
<h3>wasm linear memory ({{len .Deep.HeapSamples}} samples{{if gt .Deep.HeapSamplesDropped 0}} <span class="dropped">+{{.Deep.HeapSamplesDropped}} dropped</span>{{end}})</h3>
<table class="summary">
<tr><td>min (bytes)</td><td>{{.Deep.HeapMin}}</td></tr>
<tr><td>max (bytes)</td><td>{{.Deep.HeapMax}}</td></tr>
<tr><td>final (bytes)</td><td>{{.Deep.HeapFinal}}</td></tr>
</table>
<details>
<summary>per-sample curve</summary>
<table>
<thead><tr><th>relative (ms)</th><th>memory (bytes)</th></tr></thead>
<tbody>
{{range .Deep.HeapSamples}}
<tr><td>{{.RelativeNanos}}</td><td>{{.MemoryBytes}}</td></tr>
{{end}}
</tbody>
</table>
</details>
{{end}}
{{end}}

{{if .NativeSamples}}
<h2>native samples ({{len .NativeSamples}})</h2>
<table>
<thead><tr><th>#</th><th>relative (ms)</th><th>function</th></tr></thead>
<tbody>
{{range .NativeSamples}}
<tr>
  <td>{{.Idx}}</td>
  <td>{{.StartMs}}</td>
  <td><code>{{.Function}}</code></td>
</tr>
{{end}}
</tbody>
</table>
{{end}}

<h2>hostcall spans ({{len .Spans}}{{if gt .Dropped 0}} <span class="dropped">+{{.Dropped}} dropped</span>{{end}})</h2>
{{if .Spans}}
<table>
<thead><tr><th>#</th><th>name</th><th>start (ms)</th><th>duration (ms)</th><th>rc</th><th>tags</th></tr></thead>
<tbody>
{{range .Spans}}
<tr>
  <td>{{.Idx}}</td>
  <td><code>{{.Name}}</code></td>
  <td>{{.StartMs}}</td>
  <td>{{.DurationMs}}</td>
  <td>{{.RC}}</td>
  <td><code class="muted">{{.Tags}}</code></td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<p class="muted">no hostcall spans recorded</p>
{{end}}
</body>
</html>
`))
