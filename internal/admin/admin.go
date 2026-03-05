// Package admin provides a thread-safe store for announcements, trivia, and
// duration settings, along with an HTTP admin interface for editing them live.
// Changes persist to a JSON file and take effect immediately without restart.
package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	ann "github.com/zerohalo/weatherrupert/internal/announcements"
	"github.com/zerohalo/weatherrupert/internal/apistats"
	"github.com/zerohalo/weatherrupert/internal/config"
	"github.com/zerohalo/weatherrupert/internal/trivia"
)

// PipelineInfo is a snapshot of one active pipeline for the admin dashboard.
type PipelineInfo struct {
	ZIP         string
	Location    string
	ClockFormat string        // "12" or "24"
	Units       string        // "imperial" or "metric"
	Alerts      int           // number of active weather alerts
	Viewers     int           // current direct MPEG-TS stream viewers
	Views       int           // total direct stream connections since pipeline started
	HLSViewers  int           // current HLS viewers (0 or 1 when segmenter is subscribed)
	HLSViews    int           // total HLS viewer sessions
	MusicStream string        // display name of the music source in use
	ViewTime    time.Duration // cumulative direct stream viewing time
	HLSViewTime time.Duration // cumulative HLS viewing time
	LastSeen    time.Time     // zero means active now; non-zero is when last viewer left
	StreamURL   string        // request URL that activated this pipeline
	SlowFrames  int64         // renderer slow frame writes (>200ms)
	FFmpegWarns int64         // FFmpeg stderr warning lines
	AudioDrops  int64         // music relay audio chunk drops
	ClientDrops int64         // hub-level chunk drops for slow clients

	// Stream Health diagnostics (MPEG-TS broadcast counters).
	StreamChunks   int64
	StreamKBps     float64
	StreamSecSince float64

	// HLS Health diagnostics.
	HLSSegCount      int     // total segments produced
	HLSSegSizeAvg    int     // average segment size in bytes
	HLSSecSinceSeg   float64 // seconds since last segment finalized
	HLSSegmentMisses int64   // segment 404s (expired from ring)
	HLSLagAvg        float64 // avg segments behind live edge
}

// StreamEntry is a named music stream URL.
// Name is optional; the URL is used as the display label when Name is empty.
type StreamEntry struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url"`
}

// DisplayName returns the Name if set, otherwise the URL.
func (e StreamEntry) DisplayName() string {
	if e.Name != "" {
		return e.Name
	}
	return e.URL
}

// annJSON is the on-disk representation of an announcement, supporting both
// plain text entries (for backwards compatibility) and text+date entries.
type annJSON struct {
	Text string `json:"text"`
	Date string `json:"date,omitempty"` // "MM-DD" or empty
}

// persistedData is the on-disk JSON structure.
// Announcements and Streams use json.RawMessage so loadFromDisk can handle
// format migrations (legacy plain-string arrays vs current object arrays).
type persistedData struct {
	SlideDuration        string          `json:"slideDuration"`
	AnnouncementDuration string          `json:"announcementDuration"`
	AnnouncementInterval int             `json:"announcementInterval"`
	TriviaDuration       string          `json:"triviaDuration"`
	TriviaInterval       int             `json:"triviaInterval"`
	TriviaRandomize      bool            `json:"triviaRandomize"`
	TriviaAPI            *bool           `json:"triviaAPI,omitempty"`
	TriviaAPIAmount      *int            `json:"triviaAPIAmount,omitempty"`
	TriviaAPICategory    *int            `json:"triviaAPICategory,omitempty"`
	TriviaAPIDifficulty  *string         `json:"triviaAPIDifficulty,omitempty"`
	TriviaAPIRefresh     *string         `json:"triviaAPIRefresh,omitempty"`
	TriviaBuiltin        *bool           `json:"triviaBuiltin,omitempty"`
	PlanetLiveAlways     *bool           `json:"planetLiveAlways,omitempty"`
	SatelliteProduct     string          `json:"satelliteProduct,omitempty"` // "IR" or "VIS"
	ClockFormat          string          `json:"clockFormat,omitempty"`      // "12" or "24"
	UnitSystem           string          `json:"unitSystem,omitempty"`       // "imperial" or "metric"
	Streams              json.RawMessage `json:"streams,omitempty"`          // current: []StreamEntry
	OldStreamURLs        []string        `json:"streamURLs,omitempty"`       // legacy: []string (read-only migration)
	Announcements        json.RawMessage `json:"announcements,omitempty"`
	Trivia               []triviaJSON    `json:"trivia"`
}

type triviaJSON struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Store is a thread-safe store for announcements, trivia, duration settings,
// and music stream entries.
type Store struct {
	mu                  sync.RWMutex
	path                string
	announcements       []ann.Announcement
	triviaItems         []trivia.TriviaItem
	slideDur            time.Duration
	annDur              time.Duration
	annInterval         int // weather cycles between announcement slides; 0 = disabled
	triviaDur           time.Duration
	triviaInterval      int                 // weather cycles between trivia slides; 0 = disabled
	triviaRandomize     bool                // when true, questions are drawn from a shuffled deck
	triviaAPI           bool                // when true, include API-fetched trivia in the pool
	triviaAPIAmount     int                 // number of questions to fetch (default 50)
	triviaAPICategory   int                 // 0 = any; 9–32 = specific category
	triviaAPIDifficulty string              // "" = any; "easy", "medium", "hard"
	triviaAPIRefresh    time.Duration       // how often to re-fetch from API; 0 = startup only
	triviaBuiltin       bool                // when true, include built-in/admin trivia in the pool
	triviaAPIItems      []trivia.TriviaItem // populated once at startup by SetAPITrivia
	planetLiveAlways    bool                // when true, night sky always shows current positions
	satelliteProduct    string              // "IR" (infrared) or "VIS" (visible); default: "IR"
	clockFormat         string              // default clock format: "12" or "24"
	unitSystem          string              // default unit system: "imperial" or "metric"
	streams             []StreamEntry       // music streams; one is chosen at random per pipeline

	startedAt time.Time // container start time for uptime display

	// getPipelines is set by the manager after startup so the dashboard can
	// display active streams. May be nil if not wired up yet.
	getPipelines func() []PipelineInfo

	// getAPIStats returns lifetime API byte/request counters.
	getAPIStats func() []apistats.ServiceStat

	// getSystemStats returns host load average, container CPU percentage,
	// and the number of CPU cores allocated to the container.
	getSystemStats func() (loadAvg [3]float64, cpuPct float64, cpuCores float64)
}

// NewStore creates a Store with initial data from the given slices and defaults.
// If path exists on disk, persisted values override the supplied defaults.
// defaultStreams pre-populates the stream list until the user saves their own.
func NewStore(path string, anns []ann.Announcement, triviaItems []trivia.TriviaItem,
	defaultSlide, defaultAnn, defaultTrivia time.Duration,
	defaultAnnInterval, defaultTriviaInterval int,
	defaultStreams []StreamEntry,
	defaultTriviaAPI bool, defaultTriviaAPIAmount, defaultTriviaAPICategory int,
	defaultTriviaAPIDifficulty string, defaultTriviaAPIRefresh time.Duration,
	defaultTriviaBuiltin bool) *Store {
	s := &Store{
		path:                path,
		startedAt:           time.Now(),
		announcements:       anns,
		triviaItems:         triviaItems,
		slideDur:            defaultSlide,
		annDur:              defaultAnn,
		annInterval:         defaultAnnInterval,
		triviaDur:           defaultTrivia,
		triviaInterval:      defaultTriviaInterval,
		triviaRandomize:     true,
		triviaAPI:           defaultTriviaAPI,
		triviaAPIAmount:     defaultTriviaAPIAmount,
		triviaAPICategory:   defaultTriviaAPICategory,
		triviaAPIDifficulty: defaultTriviaAPIDifficulty,
		triviaAPIRefresh:    defaultTriviaAPIRefresh,
		triviaBuiltin:       defaultTriviaBuiltin,
		satelliteProduct:    config.SatelliteIR,
		clockFormat:         config.ClockFormat24h,
		unitSystem:          config.UnitsImperial,
		streams:             append([]StreamEntry(nil), defaultStreams...),
	}
	s.loadFromDisk()
	return s
}

// SetPipelineSource wires the dashboard to a live pipeline query function.
// Call this once after the manager is created.
func (s *Store) SetPipelineSource(fn func() []PipelineInfo) {
	s.mu.Lock()
	s.getPipelines = fn
	s.mu.Unlock()
}

// SetAPIStatsSource wires the dashboard to a live API stats query function.
func (s *Store) SetAPIStatsSource(fn func() []apistats.ServiceStat) {
	s.mu.Lock()
	s.getAPIStats = fn
	s.mu.Unlock()
}

// SetSystemStatsSource wires the dashboard to a live system stats query function.
func (s *Store) SetSystemStatsSource(fn func() (loadAvg [3]float64, cpuPct float64, cpuCores float64)) {
	s.mu.Lock()
	s.getSystemStats = fn
	s.mu.Unlock()
}

func (s *Store) loadFromDisk() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("admin: load %s: %v", s.path, err)
		}
		return
	}
	var pd persistedData
	if err := json.Unmarshal(data, &pd); err != nil {
		log.Printf("admin: parse %s: %v", s.path, err)
		return
	}
	if pd.SlideDuration != "" {
		if d, err := time.ParseDuration(pd.SlideDuration); err == nil {
			s.slideDur = d
		}
	}
	if pd.AnnouncementDuration != "" {
		if d, err := time.ParseDuration(pd.AnnouncementDuration); err == nil {
			s.annDur = d
		}
	}
	// Interval 0 is valid (disabled), so always load it.
	s.annInterval = pd.AnnouncementInterval
	if pd.TriviaDuration != "" {
		if d, err := time.ParseDuration(pd.TriviaDuration); err == nil {
			s.triviaDur = d
		}
	}
	s.triviaInterval = pd.TriviaInterval
	s.triviaRandomize = pd.TriviaRandomize
	if pd.TriviaAPI != nil {
		s.triviaAPI = *pd.TriviaAPI
	}
	if pd.TriviaBuiltin != nil {
		s.triviaBuiltin = *pd.TriviaBuiltin
	}
	if pd.TriviaAPIAmount != nil {
		s.triviaAPIAmount = *pd.TriviaAPIAmount
	}
	if pd.TriviaAPICategory != nil {
		s.triviaAPICategory = *pd.TriviaAPICategory
	}
	if pd.TriviaAPIDifficulty != nil {
		s.triviaAPIDifficulty = *pd.TriviaAPIDifficulty
	}
	if pd.TriviaAPIRefresh != nil {
		if d, err := time.ParseDuration(*pd.TriviaAPIRefresh); err == nil && d >= 0 {
			s.triviaAPIRefresh = d
		}
	}
	if pd.PlanetLiveAlways != nil {
		s.planetLiveAlways = *pd.PlanetLiveAlways
	}
	if pd.SatelliteProduct == config.SatelliteIR || pd.SatelliteProduct == config.SatelliteVIS {
		s.satelliteProduct = pd.SatelliteProduct
	}
	if pd.ClockFormat == config.ClockFormat12h || pd.ClockFormat == config.ClockFormat24h {
		s.clockFormat = pd.ClockFormat
	}
	if pd.UnitSystem == config.UnitsImperial || pd.UnitSystem == config.UnitsMetric {
		s.unitSystem = pd.UnitSystem
	}
	// Streams: try new []StreamEntry format (pd.Streams), then migrate from
	// legacy []string format (pd.OldStreamURLs). If neither is present in the
	// file, keep the compiled-in defaults unchanged.
	if len(pd.Streams) > 0 {
		var entries []StreamEntry
		if err := json.Unmarshal(pd.Streams, &entries); err == nil {
			s.streams = entries
		}
	} else if pd.OldStreamURLs != nil {
		// Migrate: plain URL strings → StreamEntry with empty names.
		s.streams = make([]StreamEntry, 0, len(pd.OldStreamURLs))
		for _, u := range pd.OldStreamURLs {
			if u = strings.TrimSpace(u); u != "" {
				s.streams = append(s.streams, StreamEntry{URL: u})
			}
		}
	}

	// Announcements: try current []annJSON format first, fall back to legacy
	// []string format so existing settings files aren't broken on upgrade.
	if len(pd.Announcements) > 0 {
		var newAnns []annJSON
		if err := json.Unmarshal(pd.Announcements, &newAnns); err == nil {
			s.announcements = make([]ann.Announcement, 0, len(newAnns))
			for _, a := range newAnns {
				if t := strings.TrimSpace(a.Text); t != "" {
					s.announcements = append(s.announcements, ann.Announcement{Text: t, Date: a.Date})
				}
			}
		} else {
			// Legacy: plain string array saved by an older version.
			var oldAnns []string
			if err2 := json.Unmarshal(pd.Announcements, &oldAnns); err2 == nil {
				s.announcements = make([]ann.Announcement, 0, len(oldAnns))
				for _, text := range oldAnns {
					if t := strings.TrimSpace(text); t != "" {
						s.announcements = append(s.announcements, ann.Announcement{Text: t})
					}
				}
			}
		}
	}
	if pd.Trivia != nil {
		s.triviaItems = make([]trivia.TriviaItem, 0, len(pd.Trivia))
		for _, item := range pd.Trivia {
			if item.Question != "" && item.Answer != "" {
				s.triviaItems = append(s.triviaItems, trivia.TriviaItem{
					Question: item.Question,
					Answer:   item.Answer,
				})
			}
		}
	}
	log.Printf("admin: loaded settings from %s", s.path)
}

func (s *Store) saveToDisk() error {
	s.mu.RLock()
	var annObjs []annJSON
	for _, a := range s.announcements {
		annObjs = append(annObjs, annJSON{Text: a.Text, Date: a.Date})
	}
	annBytes, _ := json.Marshal(annObjs)

	var trivObjs []triviaJSON
	for _, t := range s.triviaItems {
		trivObjs = append(trivObjs, triviaJSON{Question: t.Question, Answer: t.Answer})
	}

	streamsSnap := make([]StreamEntry, len(s.streams))
	copy(streamsSnap, s.streams)
	streamsBytes, _ := json.Marshal(streamsSnap)

	pd := persistedData{
		SlideDuration:        s.slideDur.String(),
		AnnouncementDuration: s.annDur.String(),
		AnnouncementInterval: s.annInterval,
		TriviaDuration:       s.triviaDur.String(),
		TriviaInterval:       s.triviaInterval,
		TriviaRandomize:      s.triviaRandomize,
		TriviaAPI:            ptrBool(s.triviaAPI),
		TriviaAPIAmount:      ptrInt(s.triviaAPIAmount),
		TriviaAPICategory:    ptrInt(s.triviaAPICategory),
		TriviaAPIDifficulty:  ptrStr(s.triviaAPIDifficulty),
		TriviaAPIRefresh:     ptrStr(s.triviaAPIRefresh.String()),
		TriviaBuiltin:        ptrBool(s.triviaBuiltin),
		PlanetLiveAlways:     ptrBool(s.planetLiveAlways),
		SatelliteProduct:     s.satelliteProduct,
		ClockFormat:          s.clockFormat,
		UnitSystem:           s.unitSystem,
		Streams:              json.RawMessage(streamsBytes),
		Announcements:        json.RawMessage(annBytes),
		Trivia:               trivObjs,
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(pd, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename to %s: %w", s.path, err)
	}
	return nil
}

// ── Snapshot / duration methods — called as closures by the renderer ──────────

// Announcements returns a point-in-time copy of the announcement list.
func (s *Store) Announcements() []ann.Announcement {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]ann.Announcement, len(s.announcements))
	copy(cp, s.announcements)
	return cp
}

// TriviaItems returns a point-in-time copy of the trivia list.
// Built-in items are included when triviaBuiltin is true; API items when triviaAPI is true.
func (s *Store) TriviaItems() []trivia.TriviaItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var cp []trivia.TriviaItem
	if s.triviaBuiltin {
		cp = make([]trivia.TriviaItem, len(s.triviaItems))
		copy(cp, s.triviaItems)
	}
	if s.triviaAPI {
		cp = append(cp, s.triviaAPIItems...)
	}
	return cp
}

// SlideDuration returns the current slide display duration.
func (s *Store) SlideDuration() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.slideDur
}

// AnnouncementDuration returns the current announcement display duration.
func (s *Store) AnnouncementDuration() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.annDur
}

// TriviaDuration returns the current trivia display duration.
func (s *Store) TriviaDuration() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.triviaDur
}

// AnnouncementInterval returns the number of complete weather cycles between
// announcement slides. 0 means announcements are disabled.
func (s *Store) AnnouncementInterval() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.annInterval
}

// TriviaInterval returns the number of complete weather cycles between trivia
// slides. 0 means trivia is disabled.
func (s *Store) TriviaInterval() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.triviaInterval
}

// TriviaRandomize reports whether trivia questions should be drawn in random order.
func (s *Store) TriviaRandomize() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.triviaRandomize
}

// TriviaAPI reports whether API-fetched trivia should be included.
func (s *Store) TriviaAPI() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.triviaAPI
}

// SetAPITrivia stores API-fetched trivia items for merging into the pool.
func (s *Store) SetAPITrivia(items []trivia.TriviaItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.triviaAPIItems = items
}

// TriviaAPIRefresh returns how often to re-fetch trivia from the API.
// Zero means startup-only (no periodic refresh).
func (s *Store) TriviaAPIRefresh() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.triviaAPIRefresh
}

// TriviaAPIOptions returns the API options for fetching trivia.
func (s *Store) TriviaAPIOptions() trivia.APIOptions {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return trivia.APIOptions{
		Count:      s.triviaAPIAmount,
		Category:   s.triviaAPICategory,
		Difficulty: s.triviaAPIDifficulty,
	}
}

// PlanetLiveAlways reports whether the night sky slide always shows live positions.
func (s *Store) PlanetLiveAlways() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.planetLiveAlways
}

// SatelliteProduct returns the GOES satellite product ("IR" or "VIS").
func (s *Store) SatelliteProduct() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.satelliteProduct
}

// ClockFormat returns the default clock format ("12" or "24").
func (s *Store) ClockFormat() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clockFormat
}

// UnitSystem returns the default unit system ("imperial" or "metric").
func (s *Store) UnitSystem() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unitSystem
}

// StreamURLs returns just the URLs from the configured stream list.
// Used by the manager to pick a random stream when starting a new pipeline.
func (s *Store) StreamURLs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	urls := make([]string, 0, len(s.streams))
	for _, e := range s.streams {
		if e.URL != "" {
			urls = append(urls, e.URL)
		}
	}
	return urls
}

// Streams returns a point-in-time copy of the full stream entry list.
func (s *Store) Streams() []StreamEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make([]StreamEntry, len(s.streams))
	copy(cp, s.streams)
	return cp
}

// ── HTTP admin interface ───────────────────────────────────────────────────────

// RegisterRoutes adds the admin handlers to mux under /admin/.
func (s *Store) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/", s.handleDashboard)
	mux.HandleFunc("GET /admin/pipelines.json", s.handlePipelinesJSON)
	mux.HandleFunc("GET /admin/announcements", s.handleAnnouncementsGet)
	mux.HandleFunc("POST /admin/announcements", s.handleAnnouncementsPost)
	mux.HandleFunc("GET /admin/trivia", s.handleTriviaGet)
	mux.HandleFunc("POST /admin/trivia", s.handleTriviaPost)
	mux.HandleFunc("GET /admin/settings", s.handleSettingsGet)
	mux.HandleFunc("POST /admin/settings", s.handleSettingsPost)
}

const pageStyle = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>WeatherRupert Admin</title>
<link rel="icon" href="/favicon.ico" type="image/png">
<style>
* { box-sizing: border-box; }
body { background: #00004E; color: #fff; font-family: monospace; margin: 0; padding: 20px 32px; }
h1 { color: #FFFF00; margin-top: 0; letter-spacing: 2px; }
h2 { color: #FFFF00; letter-spacing: 1px; }
a { color: #FFFF00; text-decoration: none; }
a:hover { text-decoration: underline; }
nav { margin-bottom: 28px; }
nav a { display: inline-block; margin-right: 24px; font-size: 1.1em; padding: 6px 0; border-bottom: 2px solid transparent; }
nav a:hover { border-color: #FFFF00; }
label { display: block; color: #aac; margin: 12px 0 4px; font-size: 0.9em; letter-spacing: 1px; }
input[type=text] { background: #000828; color: #fff; border: 1px solid #336; padding: 6px 10px; font-family: monospace; font-size: 1em; width: 280px; border-radius: 3px; }
input[type=text]:focus { outline: none; border-color: #FFFF00; }
textarea { background: #000828; color: #fff; border: 1px solid #336; padding: 8px 10px; font-family: monospace; font-size: 0.95em; border-radius: 3px; width: 100%; max-width: 700px; }
textarea:focus { outline: none; border-color: #FFFF00; }
button[type=submit], .btn-save { background: #002080; color: #FFFF00; border: 1px solid #FFFF00; padding: 8px 24px; font-family: monospace; font-size: 1em; cursor: pointer; border-radius: 3px; letter-spacing: 1px; }
button[type=submit]:hover, .btn-save:hover { background: #003090; }
.btn-add { background: #003020; color: #0f0; border: 1px solid #0a0; padding: 6px 16px; font-family: monospace; cursor: pointer; border-radius: 3px; margin-bottom: 12px; }
.btn-add:hover { background: #004030; }
.btn-del { background: transparent; color: #f44; border: 1px solid #733; padding: 3px 8px; font-family: monospace; cursor: pointer; border-radius: 3px; }
.btn-del:hover { background: #200; }
table { border-collapse: collapse; width: 100%; max-width: 900px; margin-bottom: 16px; }
th { background: #001060; color: #aac; padding: 8px 10px; text-align: left; font-size: 0.85em; letter-spacing: 1px; border-bottom: 1px solid #336; }
td { padding: 4px 6px; vertical-align: top; border-bottom: 1px solid #112; }
td input[type=text] { width: 100%; }
.hint { color: #668; font-size: 0.85em; margin: 4px 0 0; }
.msg-ok  { color: #0f0; margin-bottom: 12px; }
.msg-warn { color: #FFFF00; margin-bottom: 12px; }
.msg-err { color: #f44; margin-bottom: 12px; }
.breadcrumb { color: #668; margin-bottom: 20px; }
.breadcrumb a { color: #FFFF00; }
</style></head><body>`

func writePage(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, pageStyle)
	fmt.Fprintf(w, `<h1>WEATHER RUPERT ADMIN</h1>
<nav>
  <a href="/admin/">Dashboard</a>
  <a href="/admin/announcements">Announcements</a>
  <a href="/admin/trivia">Trivia</a>
  <a href="/admin/settings">Settings</a>
</nav>
<h2>%s</h2>
%s
</body></html>`, title, body)
}

// fmtDurationHM formats a duration as "Xh Xm", "Xm", or "<1m".
func fmtDurationHM(d time.Duration) string {
	d = d.Truncate(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	return "<1m"
}

// fmtDurationHMS formats a duration as "Xh Ym Zs", omitting zero hours/minutes.
func fmtDurationHMS(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, sec)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}

// fmtBytes formats a byte count as a human-readable string (B, KB, MB, GB).
func fmtBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// fmtUptime formats a duration as "Xd Xh Xm" or "Xh Xm Xs" for short uptimes.
func fmtUptime(d time.Duration) string {
	d = d.Truncate(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, mins, secs)
	}
	return fmt.Sprintf("%dm %ds", mins, secs)
}

// renderPipelinesHTML returns the inner HTML for the active streams section.
func (s *Store) renderAPIStatsHTML() string {
	s.mu.RLock()
	fn := s.getAPIStats
	s.mu.RUnlock()
	if fn == nil {
		return ""
	}

	stats := fn()
	if len(stats) == 0 {
		return `<p style="color:#668; margin-top:4px">No API calls recorded yet.</p>`
	}

	var apiRows strings.Builder
	var musicRows strings.Builder
	var totalReqs, totalBytes int64
	for _, st := range stats {
		if strings.HasPrefix(st.Name, "Music: ") {
			musicRows.WriteString(fmt.Sprintf(
				`<tr><td>%s</td><td style="text-align:right">%d</td><td style="text-align:right">%s</td></tr>`,
				htmlEscape(st.Name), st.Requests, fmtBytes(st.Bytes)))
			continue
		}
		totalReqs += st.Requests
		totalBytes += st.Bytes
		apiRows.WriteString(fmt.Sprintf(
			`<tr><td>%s</td><td style="text-align:right">%d</td><td style="text-align:right">%s</td></tr>`,
			htmlEscape(st.Name), st.Requests, fmtBytes(st.Bytes)))
	}
	apiRows.WriteString(fmt.Sprintf(
		`<tr style="font-weight:bold; border-top:1px solid #556"><td>Total</td><td style="text-align:right">%d</td><td style="text-align:right">%s</td></tr>`,
		totalReqs, fmtBytes(totalBytes)))

	var buf strings.Builder
	fmt.Fprintf(&buf,
		`<table style="max-width:500px; margin-top:4px">
<thead><tr><th>Service</th><th style="text-align:right">Requests</th><th style="text-align:right">Bytes</th></tr></thead>
<tbody>%s</tbody>
</table>`, apiRows.String())

	if musicRows.Len() > 0 {
		fmt.Fprintf(&buf,
			`<h4 style="color:#aac; margin:16px 0 4px">Music Streams</h4>
<table style="max-width:500px">
<thead><tr><th>Source</th><th style="text-align:right">Requests</th><th style="text-align:right">Bytes</th></tr></thead>
<tbody>%s</tbody>
</table>`, musicRows.String())
	}

	return buf.String()
}

func (s *Store) renderPipelinesHTML() string {
	s.mu.RLock()
	fn := s.getPipelines
	s.mu.RUnlock()
	if fn == nil {
		return ""
	}

	pipelines := fn()
	sort.Slice(pipelines, func(i, j int) bool {
		if pipelines[i].Viewers != pipelines[j].Viewers {
			return pipelines[i].Viewers > pipelines[j].Viewers
		}
		return pipelines[i].ZIP < pipelines[j].ZIP
	})

	totalStream := 0
	totalHLS := 0
	for _, p := range pipelines {
		totalStream += p.Viewers
		totalHLS += p.HLSViewers
	}

	if len(pipelines) == 0 {
		return `<p style="color:#668; margin-top:4px">No active streams.</p>`
	}

	var rows strings.Builder
	for _, p := range pipelines {
		streamColor := "#668"
		if p.Viewers > 0 {
			streamColor = "#FFFF00"
		}
		hlsColor := "#668"
		if p.HLSViewers > 0 {
			hlsColor = "#FFFF00"
		}
		lastSeen := `<span style="color:#FFFF00">now</span>`
		if !p.LastSeen.IsZero() {
			if p.LastSeen.Unix() == 0 {
				lastSeen = `<span style="color:#668">never</span>`
			} else {
				ago := time.Since(p.LastSeen).Truncate(time.Minute)
				lastSeen = fmt.Sprintf(`%s <span style="color:#668">(%s ago)</span>`,
					p.LastSeen.Local().Format("Jan 2 3:04 PM"), fmtDurationHM(ago))
			}
		}
		alertCell := `<span style="color:#668">0</span>`
		if p.Alerts > 0 {
			alertCell = fmt.Sprintf(`<span style="color:#f44">%d</span>`, p.Alerts)
		}
		unitsChar := "I"
		if p.Units == config.UnitsMetric {
			unitsChar = "M"
		}
		fmtCell := p.ClockFormat + " " + unitsChar
		fmtErrCell := func(v int64) string {
			if v == 0 {
				return `<span style="color:#668">0</span>`
			}
			return fmt.Sprintf(`<span style="color:#f44">%d</span>`, v)
		}
		// Stream Health: Chunks
		streamChunksCell := fmt.Sprintf("%d", p.StreamChunks)
		// Stream Health: KB/s
		var streamKBpsCell string
		if p.StreamChunks == 0 {
			streamKBpsCell = `<span style="color:#668">-</span>`
		} else {
			streamKBpsCell = fmt.Sprintf("%.0f", p.StreamKBps)
		}
		// Stream Health: Age (seconds since last broadcast)
		// Only flag as red when viewers are active; a stale age with no
		// viewers is expected (hub idles when nobody is watching).
		var streamAgeCell string
		if p.StreamChunks == 0 {
			streamAgeCell = `<span style="color:#668">-</span>`
		} else if p.Viewers > 0 && p.StreamSecSince >= 2 {
			streamAgeCell = fmt.Sprintf(`<span style="color:#f44">%.0f</span>`, p.StreamSecSince)
		} else if p.Viewers == 0 && p.StreamSecSince >= 2 {
			streamAgeCell = fmt.Sprintf(`<span style="color:#668">%.0f</span>`, p.StreamSecSince)
		} else {
			streamAgeCell = fmt.Sprintf("%.0f", p.StreamSecSince)
		}
		// HLS Health: Segs
		segsCell := fmt.Sprintf(`%d`, p.HLSSegCount)
		// HLS Health: Size (avg KB)
		var sizeCell string
		if p.HLSSegCount == 0 {
			sizeCell = `<span style="color:#668">-</span>`
		} else {
			sizeCell = fmt.Sprintf("%d", p.HLSSegSizeAvg/1024)
		}
		// HLS Health: Age (seconds since last segment)
		// Only flag as red when viewers are active; a stale age with no
		// viewers is expected (segmenter idles when nobody is watching).
		var ageCell string
		if p.HLSSegCount == 0 {
			ageCell = `<span style="color:#668">-</span>`
		} else if p.HLSViewers > 0 && p.HLSSecSinceSeg >= 6 {
			ageCell = fmt.Sprintf(`<span style="color:#f44">%.0f</span>`, p.HLSSecSinceSeg)
		} else if p.HLSViewers == 0 && p.HLSSecSinceSeg >= 6 {
			ageCell = fmt.Sprintf(`<span style="color:#668">%.0f</span>`, p.HLSSecSinceSeg)
		} else {
			ageCell = fmt.Sprintf("%.0f", p.HLSSecSinceSeg)
		}
		// HLS Health: 404s
		missCell := fmtErrCell(p.HLSSegmentMisses)
		// HLS Health: Lag
		var lagCell string
		if p.HLSLagAvg > 2 {
			lagCell = fmt.Sprintf(`<span style="color:#f44">%.1f</span>`, p.HLSLagAvg)
		} else if p.HLSLagAvg > 1 {
			lagCell = fmt.Sprintf(`<span style="color:#ff0">%.1f</span>`, p.HLSLagAvg)
		} else {
			lagCell = fmt.Sprintf("%.1f", p.HLSLagAvg)
		}

		rows.WriteString(fmt.Sprintf(
			`<tr><td><a href="%s" style="color:#FFFF00">%s</a></td>`+
				`<td style="text-align:center; color:#668">%s</td>`+
				`<td>%s</td>`+
				`<td style="text-align:center">%s</td>`+
				`<td>%s</td>`+
				`<td style="color:%s; text-align:right; border-left:1px solid #224">%d</td>`+
				`<td style="text-align:right">%d</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="text-align:right; border-left:1px solid #224">%s</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="color:%s; text-align:right; border-left:1px solid #224">%d</td>`+
				`<td style="text-align:right">%d</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="text-align:right; border-left:1px solid #224">%s</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="text-align:right; border-left:1px solid #224">%s</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="text-align:right">%s</td>`+
				`<td style="border-left:1px solid #224">%s</td></tr>`,
			htmlEscape(p.StreamURL), htmlEscape(p.ZIP), fmtCell, htmlEscape(p.Location), alertCell, htmlEscape(p.MusicStream),
			streamColor, p.Viewers, p.Views, fmtDurationHMS(p.ViewTime),
			streamChunksCell, streamKBpsCell, streamAgeCell,
			hlsColor, p.HLSViewers, p.HLSViews, fmtDurationHMS(p.HLSViewTime),
			segsCell, sizeCell, ageCell, missCell, lagCell,
			fmtErrCell(p.SlowFrames), fmtErrCell(p.FFmpegWarns), fmtErrCell(p.AudioDrops), fmtErrCell(p.ClientDrops),
			lastSeen))
	}

	return fmt.Sprintf(
		`<table style="max-width:1600px; margin-top:4px">
<thead><tr>
  <th style="width:5%%">ZIP</th>
  <th style="width:4%%">Fmt</th>
  <th style="width:10%%">Location</th>
  <th style="width:3%%">Alerts</th>
  <th style="width:8%%">Music</th>
  <th colspan="3" style="text-align:center; border-left:1px solid #336; width:12%%">MPEG-TS</th>
  <th colspan="3" style="text-align:center; border-left:1px solid #336; width:9%%">Stream Health</th>
  <th colspan="3" style="text-align:center; border-left:1px solid #336; width:12%%">HLS</th>
  <th colspan="5" style="text-align:center; border-left:1px solid #336; width:15%%">HLS Health</th>
  <th colspan="4" style="text-align:center; border-left:1px solid #336; width:12%%">Errors</th>
  <th style="border-left:1px solid #336">Last Seen</th>
</tr>
<tr>
  <th></th><th></th><th></th><th></th><th></th>
  <th style="text-align:right; border-left:1px solid #336">Now</th>
  <th style="text-align:right">Total</th>
  <th style="text-align:right">Time</th>
  <th style="text-align:right; border-left:1px solid #336">Chunks</th>
  <th style="text-align:right">KB/s</th>
  <th style="text-align:right">Age</th>
  <th style="text-align:right; border-left:1px solid #336">Now</th>
  <th style="text-align:right">Total</th>
  <th style="text-align:right">Time</th>
  <th style="text-align:right; border-left:1px solid #336">Segs</th>
  <th style="text-align:right">Size</th>
  <th style="text-align:right">Age</th>
  <th style="text-align:right">404s</th>
  <th style="text-align:right">Lag</th>
  <th style="text-align:right; border-left:1px solid #336">Slow</th>
  <th style="text-align:right">FFmpeg</th>
  <th style="text-align:right">Audio</th>
  <th style="text-align:right">Hub</th>
  <th style="border-left:1px solid #336"></th>
</tr></thead>
<tbody>%s</tbody>
</table>
<p style="color:#668; font-size:0.85em; margin-top:6px">%d active pipeline(s) &mdash; %d MPEG-TS + %d HLS viewer(s)</p>
<div style="color:#556; font-size:0.8em; margin-top:2px; line-height:1.6">
<b>Now</b> current viewers<br>
<b>Total</b> connections since pipeline started<br>
<b>Time</b> cumulative viewing time<br>
<b>Chunks</b> total MPEG-TS chunks broadcast (0 with viewers = broken pipeline)<br>
<b>KB/s</b> avg throughput while viewers active<br>
<b>Age</b> seconds since last chunk (<span style="color:#f44">red</span> &ge;2s = data stalled)<br>
<b>Segs</b> total HLS segments produced (0 while subscribed = broken)<br>
<b>Size</b> avg segment KB (tiny = quality issue, huge = wrong GOP)<br>
<b>Age</b> seconds since last segment (<span style="color:#f44">red</span> &ge;6s = 2&times; segment duration)<br>
<b>404s</b> client requested expired segment (<span style="color:#f44">red</span> if &gt;0)<br>
<b>Lag</b> avg segments behind live edge (<span style="color:#ff0">yellow</span> &gt;1, <span style="color:#f44">red</span> &gt;2)<br>
<b>Slow</b> frame writes &gt;200ms (FFmpeg CPU-starved)<br>
<b>FFmpeg</b> stderr warning lines<br>
<b>Audio</b> relay chunks dropped (pipe backpressure)<br>
<b>Hub</b> broadcast chunks dropped (slow HTTP clients)
</div>`,
		rows.String(), len(pipelines), totalStream, totalHLS)
}

func (s *Store) handlePipelinesJSON(w http.ResponseWriter, r *http.Request) {
	html := s.renderPipelinesHTML()
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"html":   html,
		"uptime": fmtUptime(time.Since(s.startedAt)),
	}
	if statsHTML := s.renderAPIStatsHTML(); statsHTML != "" {
		resp["apiStats"] = statsHTML
	}
	s.mu.RLock()
	fn := s.getSystemStats
	s.mu.RUnlock()
	if fn != nil {
		loadAvg, cpuPct, cpuCores := fn()
		resp["loadAvg"] = fmt.Sprintf("%.2f %.2f %.2f", loadAvg[0], loadAvg[1], loadAvg[2])
		if cpuPct < 0 {
			resp["cpuPct"] = "N/A"
		} else {
			resp["cpuPct"] = fmt.Sprintf("%.1f%% of %.1f cores", cpuPct, cpuCores)
		}
	}
	data, _ := json.Marshal(resp)
	w.Write(data)
}

func (s *Store) handleDashboard(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	na := len(s.announcements)
	nt := len(s.triviaItems)
	musicStreams := make([]StreamEntry, len(s.streams))
	copy(musicStreams, s.streams)
	slide := s.slideDur
	annD := s.annDur
	annInt := s.annInterval
	trivD := s.triviaDur
	trivInt := s.triviaInterval
	trivRand := s.triviaRandomize
	trivBuiltin := s.triviaBuiltin
	trivAPI := s.triviaAPI
	trivAPIAmount := s.triviaAPIAmount
	trivAPICategory := s.triviaAPICategory
	trivAPIDifficulty := s.triviaAPIDifficulty
	trivAPIRefresh := s.triviaAPIRefresh
	clockFmt := s.clockFormat
	unitSys := s.unitSystem
	planetLive := s.planetLiveAlways
	satProd := s.satelliteProduct
	s.mu.RUnlock()

	fmtInterval := func(n int) string {
		if n == 0 {
			return "disabled"
		}
		if n == 1 {
			return "every cycle"
		}
		return fmt.Sprintf("every %d cycles", n)
	}

	var musicCell string
	if len(musicStreams) == 0 {
		musicCell = `<span style="color:#668">none configured</span>`
	} else {
		var names []string
		for _, e := range musicStreams {
			names = append(names, htmlEscape(e.DisplayName()))
		}
		musicCell = `<span style="color:#FFFF00">` + strings.Join(names, `</span>, <span style="color:#FFFF00">`) + `</span>`
	}

	yn := func(b bool) string {
		if b {
			return "Yes"
		}
		return "No"
	}

	clockDisplay := "24-hour"
	if clockFmt == "12" {
		clockDisplay = "12-hour"
	}
	unitDisplay := "Imperial"
	if unitSys == "metric" {
		unitDisplay = "Metric"
	}
	satDisplay := "Infrared"
	if satProd == "visible" {
		satDisplay = "Visible"
	}

	trivAPIDetail := yn(trivAPI)
	if trivAPI {
		catName := "any"
		catMap := map[int]string{
			9: "General Knowledge", 10: "Books", 11: "Film",
			12: "Music", 13: "Musicals", 14: "Television",
			15: "Video Games", 16: "Board Games", 17: "Science &amp; Nature",
			18: "Computers", 19: "Mathematics", 20: "Mythology",
			21: "Sports", 22: "Geography", 23: "History",
			24: "Politics", 25: "Art", 26: "Celebrities",
			27: "Animals", 28: "Vehicles", 29: "Comics",
			30: "Gadgets", 31: "Anime &amp; Manga", 32: "Cartoons",
		}
		if name, ok := catMap[trivAPICategory]; ok {
			catName = name
		}
		diff := "any"
		if trivAPIDifficulty != "" {
			diff = trivAPIDifficulty
		}
		refresh := "startup only"
		if trivAPIRefresh > 0 {
			refresh = trivAPIRefresh.String()
		}
		trivAPIDetail = fmt.Sprintf("Yes (%d, %s, %s, %s)", trivAPIAmount, catName, diff, refresh)
	}

	pipelineHTML := s.renderPipelinesHTML()
	apiStatsHTML := s.renderAPIStatsHTML()

	loadAvgStr, cpuPctStr := "N/A", "N/A"
	s.mu.RLock()
	sysFn := s.getSystemStats
	s.mu.RUnlock()
	if sysFn != nil {
		la, cpu, cores := sysFn()
		if la != [3]float64{} {
			loadAvgStr = fmt.Sprintf("%.2f %.2f %.2f", la[0], la[1], la[2])
		}
		if cpu >= 0 {
			cpuPctStr = fmt.Sprintf("%.1f%% of %.1f cores", cpu, cores)
		}
	}

	body := fmt.Sprintf(`<p>At-a-glance status for active streams and content settings.</p>
<p style="color:#888; margin:4px 0 16px">Uptime: <span id="uptime" style="color:#FFFF00">%s</span> | System Load: <span id="loadavg" style="color:#FFFF00">%s</span> | CPU: <span id="cpupct" style="color:#FFFF00">%s</span></p>
<table style="max-width:560px">
<tr><td colspan="2" style="color:#FFFF00; letter-spacing:1px; padding:10px 0 2px"><b>DISPLAY</b></td></tr>
<tr><td>Clock format</td><td style="color:#FFFF00">%s</td></tr>
<tr><td>Unit system</td><td style="color:#FFFF00">%s</td></tr>
<tr><td>Satellite</td><td style="color:#FFFF00">%s</td></tr>
<tr><td>Night sky always live</td><td style="color:#FFFF00">%s</td></tr>
<tr><td>Slide duration</td><td style="color:#FFFF00">%s</td></tr>
<tr><td colspan="2" style="color:#FFFF00; letter-spacing:1px; padding:10px 0 2px"><b>ANNOUNCEMENTS</b></td></tr>
<tr><td>Announcements</td><td style="color:#FFFF00">%d items</td></tr>
<tr><td>Announcement duration</td><td style="color:#FFFF00">%s</td></tr>
<tr><td>Announcement interval</td><td style="color:#FFFF00">%s</td></tr>
<tr><td colspan="2" style="color:#FFFF00; letter-spacing:1px; padding:10px 0 2px"><b>TRIVIA</b></td></tr>
<tr><td>Trivia questions</td><td style="color:#FFFF00">%d items</td></tr>
<tr><td>Trivia duration</td><td style="color:#FFFF00">%s</td></tr>
<tr><td>Trivia interval</td><td style="color:#FFFF00">%s</td></tr>
<tr><td>Randomize</td><td style="color:#FFFF00">%s</td></tr>
<tr><td>Built-in</td><td style="color:#FFFF00">%s</td></tr>
<tr><td>API</td><td style="color:#FFFF00">%s</td></tr>
<tr><td colspan="2" style="color:#FFFF00; letter-spacing:1px; padding:10px 0 2px"><b>MUSIC</b></td></tr>
<tr><td>Music streams</td><td>%s</td></tr>
</table>

<h3 style="color:#FFFF00; letter-spacing:1px; margin:28px 0 8px">API USAGE (LIFETIME)</h3>
<div id="apistats">%s</div>

<h3 style="color:#FFFF00; letter-spacing:1px; margin:28px 0 8px">ACTIVE STREAMS</h3>
<div id="pipelines">%s</div>
<script>
setInterval(function() {
  fetch('/admin/pipelines.json')
    .then(function(r) { return r.json(); })
    .then(function(d) {
      document.getElementById('pipelines').innerHTML = d.html;
      if (d.uptime) document.getElementById('uptime').textContent = d.uptime;
      if (d.loadAvg) document.getElementById('loadavg').textContent = d.loadAvg;
      if (d.cpuPct) document.getElementById('cpupct').textContent = d.cpuPct;
      if (d.apiStats) document.getElementById('apistats').innerHTML = d.apiStats;
    })
    .catch(function() {});
}, 5000);
</script>`,
		fmtUptime(time.Since(s.startedAt)), loadAvgStr, cpuPctStr,
		clockDisplay, unitDisplay, satDisplay, yn(planetLive), slide,
		na, annD, fmtInterval(annInt),
		nt, trivD, fmtInterval(trivInt), yn(trivRand), yn(trivBuiltin), trivAPIDetail,
		musicCell,
		apiStatsHTML,
		pipelineHTML)
	writePage(w, "DASHBOARD", body)
}

func (s *Store) handleAnnouncementsGet(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	anns := make([]ann.Announcement, len(s.announcements))
	copy(anns, s.announcements)
	s.mu.RUnlock()

	flash := ""
	if r.URL.Query().Get("saved") == "1" {
		flash = `<p class="msg-ok">&#10003; Saved successfully.</p>`
	}
	if warn := r.URL.Query().Get("warn"); warn != "" {
		flash += `<p class="msg-warn">&#9888; ` + htmlEscape(warn) + `</p>`
	}

	var rows strings.Builder
	for _, a := range anns {
		rows.WriteString(fmt.Sprintf(`<tr>
<td><input type="text" name="annDate" value="%s" placeholder="Any day" style="width:90px;text-align:center"></td>
<td><input type="text" name="annText" value="%s"></td>
<td><button type="button" class="btn-del" onclick="this.closest('tr').remove()">✕</button></td>
</tr>`, htmlEscape(a.Date), htmlEscape(a.Text)))
	}

	body := fmt.Sprintf(`%s
<form method="post">
<button type="button" class="btn-add" onclick="addAnnRow()">+ Add Announcement</button>
<table id="ann-table">
<thead><tr>
  <th style="width:12%%">Date (MM-DD)</th>
  <th style="width:81%%">Text</th>
  <th style="width:7%%"></th>
</tr></thead>
<tbody>%s</tbody>
</table>
<p class="hint">Date is optional — leave blank to show every day. Use MM-DD for annual events (e.g. 02-14 for Feb 14). Text-only entries show on all days.</p>
<button type="submit">Save Announcements</button>
</form>
<script>
function addAnnRow() {
  var tbody = document.querySelector('#ann-table tbody');
  var tr = document.createElement('tr');
  tr.innerHTML = '<td><input type="text" name="annDate" placeholder="Any day" style="width:90px;text-align:center"></td>'
    + '<td><input type="text" name="annText" placeholder="Announcement text..."></td>'
    + '<td><button type="button" class="btn-del" onclick="this.closest(\'tr\').remove()">✕</button></td>';
  tbody.appendChild(tr);
  tr.querySelectorAll('input')[1].focus();
}
</script>`, flash, rows.String())
	writePage(w, "ANNOUNCEMENTS", body)
}

func (s *Store) handleAnnouncementsPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	texts := r.Form["annText"]
	dates := r.Form["annDate"]

	var newAnns []ann.Announcement
	var badDates []string
	for i, text := range texts {
		t := strings.TrimSpace(text)
		if t == "" {
			continue
		}
		var date string
		if i < len(dates) {
			d := strings.TrimSpace(dates[i])
			if d != "" {
				if _, err := time.Parse("01-02", d); err == nil {
					date = d
				} else {
					badDates = append(badDates, d)
				}
			}
		}
		newAnns = append(newAnns, ann.Announcement{Text: t, Date: date})
	}

	s.mu.Lock()
	s.announcements = newAnns
	s.mu.Unlock()

	if err := s.saveToDisk(); err != nil {
		log.Printf("admin: save: %v", err)
	}
	dest := "/admin/announcements?saved=1"
	if len(badDates) > 0 {
		msg := "Invalid date(s) ignored: " + strings.Join(badDates, ", ") + " — use MM-DD format"
		dest += "&warn=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (s *Store) handleTriviaGet(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	items := make([]trivia.TriviaItem, len(s.triviaItems))
	copy(items, s.triviaItems)
	apiItems := make([]trivia.TriviaItem, len(s.triviaAPIItems))
	copy(apiItems, s.triviaAPIItems)
	s.mu.RUnlock()

	flash := ""
	if r.URL.Query().Get("saved") == "1" {
		flash = `<p class="msg-ok">&#10003; Saved successfully.</p>`
	}

	var rows strings.Builder
	for _, item := range items {
		rows.WriteString(fmt.Sprintf(`<tr>
<td><input type="text" name="question" value="%s"></td>
<td><input type="text" name="answer" value="%s"></td>
<td><button type="button" class="btn-del" onclick="this.closest('tr').remove()">✕</button></td>
</tr>`, htmlEscape(item.Question), htmlEscape(item.Answer)))
	}

	body := fmt.Sprintf(`%s
<form method="post">
<button type="button" class="btn-add" onclick="addRow()">+ Add Question</button>
<table id="trivia-table">
<thead><tr><th style="width:55%%">Question</th><th style="width:38%%">Answer</th><th style="width:7%%"></th></tr></thead>
<tbody>%s</tbody>
</table>
<button type="submit">Save Trivia</button>
</form>
<script>
function addRow() {
  var tbody = document.querySelector('#trivia-table tbody');
  var tr = document.createElement('tr');
  tr.innerHTML = '<td><input type="text" name="question" placeholder="Question..."></td>'
    + '<td><input type="text" name="answer" placeholder="Answer..."></td>'
    + '<td><button type="button" class="btn-del" onclick="this.closest(\'tr\').remove()">✕</button></td>';
  tbody.appendChild(tr);
  tr.querySelector('input').focus();
}
</script>`, flash, rows.String())

	// Read-only API trivia section
	var apiSection strings.Builder
	apiSection.WriteString(`<hr><h3 style="color:#ee0;letter-spacing:2px">API TRIVIA</h3>`)
	if len(apiItems) == 0 {
		apiSection.WriteString(`<p style="color:#aab">API trivia is disabled or hasn&#39;t been fetched yet.</p>`)
	} else {
		apiSection.WriteString(fmt.Sprintf(`<p style="color:#aab">%d questions from Open Trivia Database</p>`, len(apiItems)))
		apiSection.WriteString(`<table style="color:#aab"><thead><tr><th style="width:40%%">Question</th><th style="width:18%%">Answer</th><th style="width:30%%">Choices</th><th style="width:12%%">Type</th></tr></thead><tbody>`)
		for _, item := range apiItems {
			choices := strings.Join(item.Choices, " / ")
			qtype := "MC"
			if len(item.Choices) == 2 {
				qtype = "TF"
			}
			apiSection.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				htmlEscape(item.Question), htmlEscape(item.Answer), htmlEscape(choices), qtype))
		}
		apiSection.WriteString(`</tbody></table>`)
	}
	body += apiSection.String()

	writePage(w, "TRIVIA", body)
}

func (s *Store) handleTriviaPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	questions := r.Form["question"]
	answers := r.Form["answer"]

	var newItems []trivia.TriviaItem
	for i := range questions {
		q := strings.TrimSpace(questions[i])
		a := ""
		if i < len(answers) {
			a = strings.TrimSpace(answers[i])
		}
		if q != "" && a != "" {
			newItems = append(newItems, trivia.TriviaItem{Question: q, Answer: a})
		}
	}

	s.mu.Lock()
	s.triviaItems = newItems
	s.mu.Unlock()

	if err := s.saveToDisk(); err != nil {
		log.Printf("admin: save: %v", err)
	}
	http.Redirect(w, r, "/admin/trivia?saved=1", http.StatusSeeOther)
}

func (s *Store) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	slide := s.slideDur.String()
	annD := s.annDur.String()
	annInt := s.annInterval
	trivD := s.triviaDur.String()
	trivInt := s.triviaInterval
	trivRand := s.triviaRandomize
	trivAPI := s.triviaAPI
	trivAPIAmount := s.triviaAPIAmount
	trivAPICategory := s.triviaAPICategory
	trivAPIDifficulty := s.triviaAPIDifficulty
	trivAPIRefresh := s.triviaAPIRefresh.String()
	trivBuiltin := s.triviaBuiltin
	planetLive := s.planetLiveAlways
	satProd := s.satelliteProduct
	clockFmt := s.clockFormat
	unitSys := s.unitSystem
	streams := make([]StreamEntry, len(s.streams))
	copy(streams, s.streams)
	s.mu.RUnlock()

	trivRandChecked := ""
	if trivRand {
		trivRandChecked = " checked"
	}
	trivAPIChecked := ""
	if trivAPI {
		trivAPIChecked = " checked"
	}
	trivBuiltinChecked := ""
	if trivBuiltin {
		trivBuiltinChecked = " checked"
	}
	planetLiveChecked := ""
	if planetLive {
		planetLiveChecked = " checked"
	}
	// Build category <select> options.
	type catOption struct {
		ID   int
		Name string
	}
	categories := []catOption{
		{0, "Any Category"},
		{9, "General Knowledge"}, {10, "Entertainment: Books"}, {11, "Entertainment: Film"},
		{12, "Entertainment: Music"}, {13, "Entertainment: Musicals &amp; Theatres"},
		{14, "Entertainment: Television"}, {15, "Entertainment: Video Games"},
		{16, "Entertainment: Board Games"}, {17, "Science &amp; Nature"},
		{18, "Science: Computers"}, {19, "Science: Mathematics"},
		{20, "Mythology"}, {21, "Sports"}, {22, "Geography"},
		{23, "History"}, {24, "Politics"}, {25, "Art"},
		{26, "Celebrities"}, {27, "Animals"}, {28, "Vehicles"},
		{29, "Entertainment: Comics"}, {30, "Science: Gadgets"},
		{31, "Entertainment: Japanese Anime &amp; Manga"}, {32, "Entertainment: Cartoon Animations"},
	}
	var categoryOptions strings.Builder
	for _, c := range categories {
		sel := ""
		if c.ID == trivAPICategory {
			sel = " selected"
		}
		categoryOptions.WriteString(fmt.Sprintf(`<option value="%d"%s>%s</option>`, c.ID, sel, c.Name))
	}

	// Build difficulty <select> options.
	type diffOption struct {
		Value string
		Label string
	}
	difficulties := []diffOption{
		{"", "Any Difficulty"}, {"easy", "Easy"}, {"medium", "Medium"}, {"hard", "Hard"},
	}
	var difficultyOptions strings.Builder
	for _, d := range difficulties {
		sel := ""
		if d.Value == trivAPIDifficulty {
			sel = " selected"
		}
		difficultyOptions.WriteString(fmt.Sprintf(`<option value="%s"%s>%s</option>`, d.Value, sel, d.Label))
	}

	clock24Checked, clock12Checked := "", ""
	if clockFmt == config.ClockFormat12h {
		clock12Checked = " checked"
	} else {
		clock24Checked = " checked"
	}
	unitsImperialChecked, unitsMetricChecked := "", ""
	if unitSys == config.UnitsMetric {
		unitsMetricChecked = " checked"
	} else {
		unitsImperialChecked = " checked"
	}
	satIRChecked, satVISChecked := "", ""
	if satProd == config.SatelliteVIS {
		satVISChecked = " checked"
	} else {
		satIRChecked = " checked"
	}

	flash := ""
	if r.URL.Query().Get("saved") == "1" {
		flash = `<p class="msg-ok">&#10003; Saved successfully.</p>`
	}
	if warn := r.URL.Query().Get("warn"); warn != "" {
		flash += `<p class="msg-warn">&#9888; ` + htmlEscape(warn) + `</p>`
	}
	if r.URL.Query().Get("err") != "" {
		flash = `<p class="msg-err">&#10007; Invalid value — durations must be positive (e.g. 8s), intervals must be whole numbers ≥ 0.</p>`
	}

	var streamRows strings.Builder
	for _, e := range streams {
		streamRows.WriteString(fmt.Sprintf(
			`<tr>`+
				`<td><input type="text" name="streamName" value="%s" placeholder="e.g. Secret Agent"></td>`+
				`<td><input type="text" name="streamURL" value="%s" placeholder="https://..."></td>`+
				`<td><button type="button" class="btn-del" onclick="this.closest('tr').remove()">✕</button></td>`+
				`</tr>`,
			htmlEscape(e.Name), htmlEscape(e.URL)))
	}

	body := fmt.Sprintf(`%s
<form method="post">
<h3 style="color:#FFFF00; margin:0 0 12px; letter-spacing:1px">DISPLAY</h3>
<label>Clock Format</label>
<div style="margin:6px 0 4px; display:flex; gap:20px">
  <label style="display:inline-flex; align-items:center; gap:8px; cursor:pointer; color:#fff; font-weight:normal; margin:0">
    <input type="radio" name="clockFormat" value="24"%s style="accent-color:#FFFF00"> 24-hour (14:30)
  </label>
  <label style="display:inline-flex; align-items:center; gap:8px; cursor:pointer; color:#fff; font-weight:normal; margin:0">
    <input type="radio" name="clockFormat" value="12"%s style="accent-color:#FFFF00"> 12-hour (2:30 PM)
  </label>
</div>
<p class="hint">Default for new streams. Override per-request with ?clock=12 or ?clock=24 in the URL.</p>

<label>Unit System</label>
<div style="margin:6px 0 4px; display:flex; gap:20px">
  <label style="display:inline-flex; align-items:center; gap:8px; cursor:pointer; color:#fff; font-weight:normal; margin:0">
    <input type="radio" name="unitSystem" value="imperial"%s style="accent-color:#FFFF00"> Imperial (°F, mph, mi, inHg)
  </label>
  <label style="display:inline-flex; align-items:center; gap:8px; cursor:pointer; color:#fff; font-weight:normal; margin:0">
    <input type="radio" name="unitSystem" value="metric"%s style="accent-color:#FFFF00"> Metric (°C, km/h, km, hPa)
  </label>
</div>
<p class="hint">Default for new streams. Override per-request with ?units=imperial or ?units=metric in the URL.</p>

<label style="display:flex; align-items:center; gap:10px; cursor:pointer; margin-top:14px">
  <input type="checkbox" name="planetLiveAlways" value="1"%s style="width:auto; accent-color:#FFFF00">
  Night sky always live
</label>
<p class="hint">When enabled, the night sky slide always shows current planet positions. When disabled (default), it shows positions at sunset until the sun sets, then switches to live.</p>

<label>Satellite Product</label>
<div style="margin:6px 0 4px; display:flex; gap:20px">
  <label style="display:inline-flex; align-items:center; gap:8px; cursor:pointer; color:#fff; font-weight:normal; margin:0">
    <input type="radio" name="satelliteProduct" value="IR"%s style="accent-color:#FFFF00"> Infrared (day &amp; night)
  </label>
  <label style="display:inline-flex; align-items:center; gap:8px; cursor:pointer; color:#fff; font-weight:normal; margin:0">
    <input type="radio" name="satelliteProduct" value="VIS"%s style="accent-color:#FFFF00"> Visible (daytime only)
  </label>
</div>
<p class="hint">GOES satellite imagery layer. Infrared works day and night; visible is higher contrast but blank after dark.</p>

<label>Slide Duration (weather slides)</label>
<input type="text" name="slideDuration" value="%s">
<p class="hint">How long each weather slide is shown. Example: 8s</p>

<hr style="border:none; border-top:1px solid #224; margin:28px 0">
<h3 style="color:#FFFF00; margin:0 0 12px; letter-spacing:1px">ANNOUNCEMENTS</h3>
<label>Announcement Duration</label>
<input type="text" name="announcementDuration" value="%s">
<p class="hint">How long each announcement is displayed. Example: 10s</p>

<label>Announcement Interval</label>
<input type="number" name="announcementInterval" value="%d" min="0" style="width:120px">
<p class="hint">Show announcements every N weather cycles. 0 = disabled, 1 = every cycle, 2 = every other cycle, etc.</p>

<hr style="border:none; border-top:1px solid #224; margin:28px 0">
<h3 style="color:#FFFF00; margin:0 0 12px; letter-spacing:1px">TRIVIA</h3>
<label>Trivia Duration</label>
<input type="text" name="triviaDuration" value="%s">
<p class="hint">Total time per trivia question (65%%%% question, 35%%%% answer). Example: 20s</p>

<label>Trivia Interval</label>
<input type="number" name="triviaInterval" value="%d" min="0" style="width:120px">
<p class="hint">Show trivia every N weather cycles. 0 = disabled, 1 = every cycle, 2 = every other cycle, etc.</p>

<label style="display:flex; align-items:center; gap:10px; cursor:pointer; margin-top:14px">
  <input type="checkbox" name="triviaRandomize" value="1"%s style="width:auto; accent-color:#FFFF00">
  Randomize trivia order
</label>
<p class="hint">When enabled, questions are drawn from a shuffled deck — each question appears once before any repeat.</p>

<label style="display:flex; align-items:center; gap:10px; cursor:pointer; margin-top:14px">
  <input type="checkbox" name="triviaBuiltin" value="1"%s style="width:auto; accent-color:#FFFF00">
  Include built-in trivia
</label>
<p class="hint">When enabled, the default trivia questions and any admin-edited items are included in the pool.</p>

<label style="display:flex; align-items:center; gap:10px; cursor:pointer; margin-top:14px">
  <input type="checkbox" name="triviaAPI" value="1"%s style="width:auto; accent-color:#FFFF00">
  Fetch trivia from Open Trivia Database
</label>
<p class="hint">When enabled, multiple-choice questions from opentdb.com are added to the trivia pool on startup.</p>

<div style="margin-left:28px">
<label>Amount</label>
<input type="number" name="triviaAPIAmount" value="%d" min="10" max="50" style="width:120px">
<p class="hint">Number of questions to fetch (10–50).</p>

<label>Category</label>
<select name="triviaAPICategory" style="background:#000828; color:#fff; border:1px solid #336; padding:6px 10px; font-family:monospace; font-size:1em; border-radius:3px">
%s
</select>

<label>Difficulty</label>
<select name="triviaAPIDifficulty" style="background:#000828; color:#fff; border:1px solid #336; padding:6px 10px; font-family:monospace; font-size:1em; border-radius:3px">
%s
</select>

<label>API Refresh Interval</label>
<input type="text" name="triviaAPIRefresh" value="%s" style="width:120px">
<p class="hint">How often to fetch new questions. 0s = startup only. Example: 24h, 12h. Changes take effect on next server restart.</p>
</div>

<hr style="border:none; border-top:1px solid #224; margin:28px 0">
<h3 style="color:#FFFF00; margin:0 0 12px; letter-spacing:1px">MUSIC STREAMS</h3>
<button type="button" class="btn-add" onclick="addStreamRow()">+ Add Stream</button>
<table id="stream-table" style="margin-top:10px">
<thead><tr>
  <th style="width:28%%">Name (optional)</th>
  <th style="width:65%%">Stream URL</th>
  <th style="width:7%%"></th>
</tr></thead>
<tbody>%s</tbody>
</table>
<p class="hint">One stream is chosen at random for each new ZIP code pipeline. Changes apply to new streams; existing streams require a server restart. Leave empty to use local music files or silence.</p>

<br>
<button type="submit">Save Settings</button>
</form>
<script>
function addStreamRow() {
  var tbody = document.querySelector('#stream-table tbody');
  var tr = document.createElement('tr');
  tr.innerHTML = '<td><input type="text" name="streamName" placeholder="e.g. Secret Agent"></td>'
    + '<td><input type="text" name="streamURL" placeholder="https://..."></td>'
    + '<td><button type="button" class="btn-del" onclick="this.closest(\'tr\').remove()">&#x2715;</button></td>';
  tbody.appendChild(tr);
  tr.querySelector('input').focus();
}
</script>`, flash, clock24Checked, clock12Checked, unitsImperialChecked, unitsMetricChecked, planetLiveChecked, satIRChecked, satVISChecked, slide, annD, annInt, trivD, trivInt, trivRandChecked, trivBuiltinChecked, trivAPIChecked, trivAPIAmount, categoryOptions.String(), difficultyOptions.String(), trivAPIRefresh, streamRows.String())
	writePage(w, "SETTINGS", body)
}

func (s *Store) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	slideStr := strings.TrimSpace(r.FormValue("slideDuration"))
	annStr := strings.TrimSpace(r.FormValue("announcementDuration"))
	annIntStr := strings.TrimSpace(r.FormValue("announcementInterval"))
	trivStr := strings.TrimSpace(r.FormValue("triviaDuration"))
	trivIntStr := strings.TrimSpace(r.FormValue("triviaInterval"))

	slideD, err1 := time.ParseDuration(slideStr)
	annD, err2 := time.ParseDuration(annStr)
	trivD, err3 := time.ParseDuration(trivStr)

	var annInt, trivInt int
	_, err4 := fmt.Sscanf(annIntStr, "%d", &annInt)
	_, err5 := fmt.Sscanf(trivIntStr, "%d", &trivInt)

	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil ||
		slideD <= 0 || annD <= 0 || trivD <= 0 || annInt < 0 || trivInt < 0 {
		http.Redirect(w, r, "/admin/settings?err=1", http.StatusSeeOther)
		return
	}

	// Pair streamName + streamURL slices; require a valid http(s) URL to keep the entry.
	names := r.Form["streamName"]
	urls := r.Form["streamURL"]
	newStreams := []StreamEntry{}
	for i, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" || (!strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://")) {
			continue
		}
		name := ""
		if i < len(names) {
			name = strings.TrimSpace(names[i])
		}
		newStreams = append(newStreams, StreamEntry{Name: name, URL: u})
	}

	trivRand := r.FormValue("triviaRandomize") == "1"
	trivBuiltin := r.FormValue("triviaBuiltin") == "1"
	trivAPI := r.FormValue("triviaAPI") == "1"
	planetLive := r.FormValue("planetLiveAlways") == "1"

	var warnings []string

	var trivAPIAmount int
	if _, err := fmt.Sscanf(strings.TrimSpace(r.FormValue("triviaAPIAmount")), "%d", &trivAPIAmount); err != nil || trivAPIAmount < 10 {
		if trivAPIAmount != 0 || err != nil {
			warnings = append(warnings, "API amount clamped to 10 (must be 10–50)")
		}
		trivAPIAmount = 10
	}
	if trivAPIAmount > 50 {
		warnings = append(warnings, fmt.Sprintf("API amount clamped to 50 (must be 10–50)"))
		trivAPIAmount = 50
	}

	var trivAPICategory int
	fmt.Sscanf(strings.TrimSpace(r.FormValue("triviaAPICategory")), "%d", &trivAPICategory)
	if trivAPICategory < 0 || (trivAPICategory > 0 && trivAPICategory < 9) || trivAPICategory > 32 {
		warnings = append(warnings, "API category reset to Any (invalid value)")
		trivAPICategory = 0
	}

	trivAPIDifficulty := strings.TrimSpace(r.FormValue("triviaAPIDifficulty"))
	if trivAPIDifficulty != "easy" && trivAPIDifficulty != "medium" && trivAPIDifficulty != "hard" {
		if trivAPIDifficulty != "" {
			warnings = append(warnings, "API difficulty reset to Any (invalid value)")
		}
		trivAPIDifficulty = ""
	}

	trivAPIRefreshStr := strings.TrimSpace(r.FormValue("triviaAPIRefresh"))
	if trivAPIRefreshStr == "" {
		trivAPIRefreshStr = "0s"
	}
	trivAPIRefreshD, errRefresh := time.ParseDuration(trivAPIRefreshStr)
	if errRefresh != nil || trivAPIRefreshD < 0 {
		http.Redirect(w, r, "/admin/settings?err=1", http.StatusSeeOther)
		return
	}

	satProd := r.FormValue("satelliteProduct")
	if satProd != config.SatelliteIR && satProd != config.SatelliteVIS {
		warnings = append(warnings, "Satellite reset to Infrared (invalid value)")
		satProd = config.SatelliteIR
	}

	clockFmt := r.FormValue("clockFormat")
	if clockFmt != config.ClockFormat12h && clockFmt != config.ClockFormat24h {
		warnings = append(warnings, "Clock format reset to 24-hour (invalid value)")
		clockFmt = config.ClockFormat24h
	}

	unitSys := r.FormValue("unitSystem")
	if unitSys != config.UnitsImperial && unitSys != config.UnitsMetric {
		warnings = append(warnings, "Unit system reset to Imperial (invalid value)")
		unitSys = config.UnitsImperial
	}

	s.mu.Lock()
	s.slideDur = slideD
	s.annDur = annD
	s.annInterval = annInt
	s.triviaDur = trivD
	s.triviaInterval = trivInt
	s.triviaRandomize = trivRand
	s.triviaBuiltin = trivBuiltin
	s.triviaAPI = trivAPI
	s.triviaAPIAmount = trivAPIAmount
	s.triviaAPICategory = trivAPICategory
	s.triviaAPIDifficulty = trivAPIDifficulty
	s.triviaAPIRefresh = trivAPIRefreshD
	s.planetLiveAlways = planetLive
	s.satelliteProduct = satProd
	s.clockFormat = clockFmt
	s.unitSystem = unitSys
	s.streams = newStreams
	s.mu.Unlock()

	if err := s.saveToDisk(); err != nil {
		log.Printf("admin: save: %v", err)
	}
	dest := "/admin/settings?saved=1"
	if len(warnings) > 0 {
		dest += "&warn=" + url.QueryEscape(strings.Join(warnings, "; "))
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func ptrStr(s string) *string { return &s }
func ptrBool(b bool) *bool    { return &b }
func ptrInt(n int) *int       { return &n }

// htmlEscape escapes HTML special characters for safe embedding in attributes and text.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}
