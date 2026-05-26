package main

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zerohalo/weatherrupert/internal/admin"
	"github.com/zerohalo/weatherrupert/internal/apiurl"
	"github.com/zerohalo/weatherrupert/internal/config"
	"github.com/zerohalo/weatherrupert/internal/geo"
	"github.com/zerohalo/weatherrupert/internal/plog"
	"github.com/zerohalo/weatherrupert/internal/renderer"
	"github.com/zerohalo/weatherrupert/internal/stream"
	"github.com/zerohalo/weatherrupert/internal/weather"
)

// Pipeline holds all per-ZIP resources: weather client, renderer, FFmpeg, and broadcast hub.
type Pipeline struct {
	zip           string
	clockFormat   string // "12" or "24"
	units         string // "imperial" or "metric"
	musicStream   string // display name of the music source
	streamURL     string // request URL that activated this pipeline
	wc            *weather.Client
	hub           *stream.Hub
	seg           *stream.HLSSegmenter
	rnd           *renderer.Renderer
	ff            *stream.FFmpeg
	ffIsInitial   bool                      // true while ff is the pre-started silence process; the first OnActive reuses it
	lastSeen      atomic.Pointer[time.Time] // set when last viewer disconnects
	cancel        context.CancelFunc
	locationReady chan struct{}      // closed when bootstrap resolves the city name
	activeMu      sync.Mutex         // serializes OnActive/OnIdle and shutdown cleanup
	relayMu       sync.Mutex         // protects relay + relayPipe (rotated on reconnect)
	relayPipe     *os.File           // relay pipe read end (nil if not using relay)
	relay         *stream.MusicRelay // relay reference for unsubscribe on shutdown
}

// Location waits up to 15 seconds for the NWS bootstrap to resolve the city
// name, then returns it. Falls back to "ZIP XXXXX" if bootstrap hasn't
// finished or failed within the timeout.
func (p *Pipeline) Location() string {
	select {
	case <-p.locationReady:
	case <-time.After(15 * time.Second):
	}
	return p.wc.Location()
}

// Manager lazily creates and caches one Pipeline per ZIP code.
type Manager struct {
	mu           sync.Mutex
	pipelines    map[string]*Pipeline
	previewCache map[string][]byte             // ZIP → last good preview PNG
	relays       map[string]*stream.MusicRelay // keyed by stream URL
	cfg          *config.Config
	music        *stream.MusicSource
	store        *admin.Store
	rootCtx      context.Context
	httpClient   *http.Client                      // tracked HTTP client for weather APIs
	streamClient *http.Client                      // tracked HTTP client for music streams (no timeout)
	classifier   *apiurl.Classifier                // hostname→label mapper (registers stream names)
	solarData    atomic.Pointer[weather.SolarData] // shared across all pipelines
}

// NewManager creates a Manager. rootCtx should be the application shutdown context.
func NewManager(rootCtx context.Context, cfg *config.Config, music *stream.MusicSource, store *admin.Store, httpClient, streamClient *http.Client, classifier *apiurl.Classifier) *Manager {
	return &Manager{
		pipelines:    make(map[string]*Pipeline),
		previewCache: make(map[string][]byte),
		relays:       make(map[string]*stream.MusicRelay),
		cfg:          cfg,
		music:        music,
		store:        store,
		rootCtx:      rootCtx,
		httpClient:   httpClient,
		streamClient: streamClient,
		classifier:   classifier,
	}
}

// StartSolarRefresh starts the shared solar data refresh goroutine.
// Called once at application startup. Solar data is shared across all pipelines.
// Pre-seeds from any existing pipeline cache so we skip the initial fetch if fresh.
func (m *Manager) StartSolarRefresh() {
	// Scan for an existing weather cache to pre-seed solar data.
	cacheDir := filepath.Dir(m.cfg.AdminDataPath)
	entries, _ := os.ReadDir(cacheDir)
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 14 && e.Name()[:14] == "weather_cache_" {
			if solar := weather.LoadSolarFromCache(filepath.Join(cacheDir, e.Name())); solar != nil {
				m.solarData.Store(solar)
				break
			}
		}
	}
	go weather.RunSolarRefresh(m.rootCtx, m.httpClient, &m.solarData, m.HasViewers)
}

// HasViewers returns true if any pipeline has at least one connected viewer.
func (m *Manager) HasViewers() bool {
	m.mu.Lock()
	pipelines := make([]*Pipeline, 0, len(m.pipelines))
	for _, p := range m.pipelines {
		pipelines = append(pipelines, p)
	}
	m.mu.Unlock()

	for _, p := range pipelines {
		if p.hub.ClientCount() > 0 {
			return true
		}
	}
	return false
}

// ActivePipelines returns a snapshot of all currently running pipelines with
// their viewer counts. It is safe to call concurrently with Get and start.
func (m *Manager) ActivePipelines() []admin.PipelineInfo {
	m.mu.Lock()
	// Copy pipeline pointers under the lock, then release before querying each one.
	pipelines := make([]*Pipeline, 0, len(m.pipelines))
	for _, p := range m.pipelines {
		pipelines = append(pipelines, p)
	}
	m.mu.Unlock()

	infos := make([]admin.PipelineInfo, 0, len(pipelines))
	for _, p := range pipelines {
		// Hub.ClientCount() includes the HLS segmenter when subscribed, so
		// subtract it to get direct MPEG-TS stream viewers only.
		hlsSegSub := p.seg.ClientCount()
		hubClients := p.hub.ClientCount()
		directViewers := hubClients - hlsSegSub
		hlsViewers := p.seg.ViewerCount()
		totalViewers := directViewers + hlsViewers
		totalViews := p.hub.TotalViews() - p.seg.HubSubscriptions() + p.seg.TotalViews()
		var lastSeen time.Time
		if totalViewers > 0 {
			// Active now — sentinel zero time means "now".
		} else if t := p.lastSeen.Load(); t != nil {
			lastSeen = *t
		} else if totalViews == 0 {
			// Never had any viewers — show "never" instead of "now".
			// Use a sentinel time of Unix epoch to distinguish from active.
			lastSeen = time.Unix(0, 0)
		}
		var alertCount int
		if wd := p.wc.Current(); wd != nil {
			alertCount = len(wd.Alerts)
		}
		var audioDrops int64
		p.relayMu.Lock()
		if p.relay != nil && p.relayPipe != nil {
			audioDrops = p.relay.Drops(p.relayPipe)
		}
		musicStream := p.musicStream
		p.relayMu.Unlock()
		var ffWarnings int64
		if ff := p.ff; ff != nil {
			ffWarnings = ff.Warnings()
		}
		diag := p.seg.Diagnostics()
		hubDiag := p.hub.Diagnostics()
		infos = append(infos, admin.PipelineInfo{
			ZIP:              p.zip,
			Location:         p.wc.Location(),
			ClockFormat:      p.clockFormat,
			Units:            p.units,
			Alerts:           alertCount,
			Viewers:          directViewers,
			Views:            p.hub.TotalViews() - p.seg.HubSubscriptions(),
			HLSViewers:       hlsViewers,
			HLSViews:         p.seg.TotalViews(),
			MusicStream:      musicStream,
			ViewTime:         p.hub.TotalViewTime() - p.seg.HubSubscriptionTime(),
			HLSViewTime:      p.seg.TotalViewTime(),
			LastSeen:         lastSeen,
			StreamURL:        p.streamURL,
			SlowFrames:       p.rnd.SlowFrames(),
			FFmpegWarns:      ffWarnings,
			AudioDrops:       audioDrops,
			ClientDrops:      p.hub.ClientDrops(),
			StreamChunks:     hubDiag.ChunkCount,
			StreamKBps:       hubDiag.KBps,
			StreamSecSince:   hubDiag.SecSinceSend,
			HLSSegCount:      diag.SegCount,
			HLSSegSizeAvg:    diag.SegSizeAvg,
			HLSSecSinceSeg:   diag.SecSinceSeg,
			HLSSegmentMisses: diag.SegmentMisses,
			HLSLagAvg:        diag.LagAvg,
		})
	}
	return infos
}

// Lookup returns an existing pipeline for the given parameters without creating
// one. Returns nil if no matching pipeline exists.
func (m *Manager) Lookup(zip, clockFormat, units, tz string) *Pipeline {
	if clockFormat != config.ClockFormat12h && clockFormat != config.ClockFormat24h {
		clockFormat = config.ClockFormat24h
	}
	if units != config.UnitsImperial && units != config.UnitsMetric {
		units = config.UnitsImperial
	}
	loc, err := geo.Lookup(zip)
	if err != nil {
		return nil
	}
	tzName := loc.TimeLocation().String()
	if tz != "" {
		if parsed, err := time.LoadLocation(tz); err == nil {
			tzName = parsed.String()
		}
	}
	key := loc.ZipCode + "#" + clockFormat + "#" + units + "#" + tzName

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pipelines[key]
}

// Get returns the pipeline for the given ZIP code and clock format, creating it
// on first call. Subsequent calls for the same (ZIP, clockFormat, units, tz)
// tuple return the cached pipeline immediately. clockFormat must be "12" or
// "24"; any other value is normalised to "24". tz is an IANA timezone name
// (e.g. "America/New_York"); empty or invalid values fall back to time.Local.
// An error is returned only if the ZIP is invalid.
func (m *Manager) Get(zip, clockFormat, units, tz string) (*Pipeline, error) {
	if clockFormat != config.ClockFormat12h && clockFormat != config.ClockFormat24h {
		clockFormat = config.ClockFormat24h
	}
	if units != config.UnitsImperial && units != config.UnitsMetric {
		units = config.UnitsImperial
	}
	// Validate the ZIP against the database before acquiring the lock,
	// so bad ZIPs fail fast without holding the mutex.
	loc, err := geo.Lookup(zip)
	if err != nil {
		return nil, fmt.Errorf("unknown ZIP code %q", zip)
	}

	// Validate timezone; fall back to the ZIP's inferred timezone for empty/invalid values.
	tzLoc := loc.TimeLocation()
	if tz != "" {
		if parsed, err := time.LoadLocation(tz); err == nil {
			tzLoc = parsed
		}
	}
	tzName := tzLoc.String()

	key := loc.ZipCode + "#" + clockFormat + "#" + units + "#" + tzName

	m.mu.Lock()
	defer m.mu.Unlock()

	if p, ok := m.pipelines[key]; ok {
		return p, nil
	}

	p, err := m.start(loc, clockFormat, units, tzLoc)
	if err != nil {
		return nil, err
	}
	m.pipelines[key] = p
	return p, nil
}

// Peek returns an existing pipeline for the given ZIP without creating one.
// It first tries the exact clock/units/tz match, then falls back to any pipeline
// for the same ZIP. Returns nil if no pipeline exists.
func (m *Manager) Peek(zip, clockFormat, units, tz string) *Pipeline {
	if clockFormat != config.ClockFormat12h && clockFormat != config.ClockFormat24h {
		clockFormat = config.ClockFormat24h
	}
	if units != config.UnitsImperial && units != config.UnitsMetric {
		units = config.UnitsImperial
	}
	loc, err := geo.Lookup(zip)
	if err != nil {
		return nil
	}
	tzName := loc.TimeLocation().String()
	if tz != "" {
		if parsed, err := time.LoadLocation(tz); err == nil {
			tzName = parsed.String()
		}
	}
	key := loc.ZipCode + "#" + clockFormat + "#" + units + "#" + tzName

	m.mu.Lock()
	defer m.mu.Unlock()

	// Exact match.
	if p, ok := m.pipelines[key]; ok {
		return p
	}
	// Any pipeline for this ZIP (different clock/units/tz variant).
	prefix := loc.ZipCode + "#"
	for k, p := range m.pipelines {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			return p
		}
	}
	return nil
}

// CachedPreview returns the manager-level cached preview PNG for a ZIP,
// or nil if none has been stored yet.
func (m *Manager) CachedPreview(zip string) []byte {
	loc, err := geo.Lookup(zip)
	if err != nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.previewCache[loc.ZipCode]
}

// StoreCachedPreview saves a preview PNG for a ZIP at the manager level.
func (m *Manager) StoreCachedPreview(zip string, png []byte) {
	loc, err := geo.Lookup(zip)
	if err != nil {
		return
	}
	m.mu.Lock()
	m.previewCache[loc.ZipCode] = png
	m.mu.Unlock()
}

// start launches all goroutines for a new pipeline. The pipeline is returned
// immediately; weather bootstrapping runs in the background so the stream
// begins serving a "Loading..." slide right away. clockFormat is "12" or "24".
func (m *Manager) start(loc geo.Location, clockFormat, units string, tzLoc *time.Location) (*Pipeline, error) {
	pl := plog.New("pipeline", loc.ZipCode)
	cityLabel := fmt.Sprintf("%s, %s", loc.City, loc.State)
	getSatProduct := func(frameTime time.Time) string {
		prod := m.store.SatelliteProduct()
		if prod == config.SatelliteAuto {
			h := frameTime.In(tzLoc).Hour()
			if h >= 7 && h < 19 {
				return config.SatelliteVIS
			}
			return config.SatelliteIR
		}
		return prod
	}
	wc := weather.NewClient(m.cfg.WeatherAPIURL, loc.Lat, loc.Lon, cityLabel, loc.ZipCode, m.cfg.Frames, m.cfg.Radius, getSatProduct, m.solarData.Load, m.httpClient, tzLoc)
	cacheDir := filepath.Dir(m.cfg.AdminDataPath)
	wc.SetCachePath(filepath.Join(cacheDir, fmt.Sprintf("weather_cache_%s.json", loc.ZipCode)))

	hub := stream.NewHub()

	// Derive a child context so this pipeline can be cancelled independently
	// (e.g. on shutdown). Currently we cancel on root context only.
	ctx, cancel := context.WithCancel(m.rootCtx)

	seg := stream.NewHLSSegmenter(hub, loc.ZipCode, clockFormat, units, m.cfg.HLSSegmentDuration, m.cfg.HLSPlaylistSize, m.cfg.HLSRingSize)
	go seg.Run(ctx)

	// Build the canonical stream URL for the admin dashboard.
	reqURL := fmt.Sprintf("/stream?zip=%s", loc.ZipCode)
	if clockFormat != config.ClockFormat24h {
		reqURL += "&clock=" + clockFormat
	}
	if units != config.UnitsImperial {
		reqURL += "&units=" + units
	}

	locationReady := make(chan struct{})
	p := &Pipeline{
		zip:           loc.ZipCode,
		clockFormat:   clockFormat,
		units:         units,
		musicStream:   "Silence",
		streamURL:     reqURL,
		wc:            wc,
		hub:           hub,
		seg:           seg,
		cancel:        cancel,
		locationReady: locationReady,
	}

	// Bootstrap + refresh loop (background — stream shows loading slide until ready).
	// If a fresh cache exists, restore from it and skip bootstrap entirely.
	// locationReady is closed on exit (success or failure) so Location() never hangs.
	go func() {
		defer close(locationReady)
		if !wc.RestoreFromCache(m.cfg.WeatherRefresh) {
			if err := wc.Bootstrap(ctx); err != nil {
				if ctx.Err() == nil {
					pl.Printf("bootstrap failed: %v", err)
				}
				return
			}
		}
		// Seed the shared solar pointer from cached data if we don't have any yet.
		if cur := wc.Current(); cur != nil && cur.Solar != nil {
			if existing := m.solarData.Load(); existing == nil {
				m.solarData.Store(cur.Solar)
			}
		}
		pl.Printf("weather ready (%s)", wc.Location())
		go wc.Run(ctx, m.cfg.WeatherRefresh, func() bool { return hub.ClientCount() > 0 })
	}()

	// startFFmpeg launches a fresh FFmpeg process with the given music source,
	// wires up the hub reader, and points the renderer at the new stdin.
	// It waits for any previous hub reader goroutine to exit first so there
	// is never more than one goroutine broadcasting to the hub.
	var hubReaderDone chan struct{}
	startFFmpeg := func(music *stream.MusicSource, label string) (*stream.FFmpeg, error) {
		// Wait for the previous hub reader to finish (it exits on EOF
		// when the old FFmpeg's stdout pipe closes after Kill).
		if hubReaderDone != nil {
			select {
			case <-hubReaderDone:
			case <-time.After(3 * time.Second):
				pl.Printf("WARNING: hub reader did not exit in 3s, proceeding anyway")
			}
		}

		newFF, err := stream.Start(m.cfg.Width, m.cfg.Height, m.cfg.FrameRate, music, loc.ZipCode, m.cfg.VideoMaxRate)
		if err != nil {
			return nil, err
		}
		seg.ResetAccumulator()
		if p.rnd != nil {
			p.rnd.SetOutput(newFF.Stdin())
		}
		done := make(chan struct{})
		hubReaderDone = done
		go func() {
			hub.Run(newFF.Stdout())
			close(done)
		}()
		pl.Printf("ffmpeg started (%s)", label)
		return newFF, nil
	}

	// Kill FFmpeg when no viewers are connected; start a fresh process when
	// viewers reconnect.  This eliminates stale audio: a new FFmpeg process
	// has no internal queue from a previous stream.
	hub.OnActive = func() {
		p.activeMu.Lock()
		defer p.activeMu.Unlock()

		// Phase 1: ensure FFmpeg is running with silence so the viewer sees
		// video immediately. On the very first activation reuse the FFmpeg that
		// start() already launched — the renderer is wired to its stdin and it
		// is producing loading frames, so killing and restarting it here would
		// only add FFmpeg startup latency (plus up to a 3s wait for the previous
		// hub reader to drain) before the first frame reaches the viewer.
		if p.ffIsInitial && p.ff != nil {
			p.ffIsInitial = false
		} else {
			// Kill any leftover FFmpeg from a previous cycle and start fresh.
			if p.ff != nil {
				p.ff.Kill()
				p.ff = nil
			}
			silenceFF, err := startFFmpeg(stream.NewSilenceSource(), "silence")
			if err != nil {
				pl.Printf("ffmpeg start (silence): %v", err)
				return
			}
			p.ff = silenceFF
		}

		// Phase 2 (background): connect a music relay, then restart FFmpeg
		// with the relay pipe so the viewer hears music.
		go func() {
			streamURL, musicName := m.pickStream(p)
			if streamURL == "" {
				// No stream configured — stay on silence (or local files).
				return
			}

			m.mu.Lock()
			relay := m.relays[streamURL]
			if relay == nil {
				relay = stream.NewMusicRelay(streamURL, m.streamClient)
				m.relays[streamURL] = relay
			}
			m.mu.Unlock()

			relayPipe := relay.Subscribe()
			if relayPipe == nil {
				pl.Printf("relay pipe failed for %s", streamURL)
				return
			}
			relay.SetActive(relayPipe, true)

			// Wait for the relay to actually connect (with timeout).
			waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Second)
			defer waitCancel()
			// Poll until either the relay is streaming, viewers leave, or timeout.
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-waitCtx.Done():
					pl.Printf("music relay connect timeout for %s — staying on silence", streamURL)
					relay.Unsubscribe(relayPipe)
					return
				case <-ticker.C:
				}
				if hub.ClientCount() == 0 {
					relay.Unsubscribe(relayPipe)
					return // viewer left before music started
				}
				if relay.Received(relayPipe) > 0 {
					break // relay is connected and sending data
				}
			}

			p.activeMu.Lock()
			defer p.activeMu.Unlock()

			if hub.ClientCount() == 0 {
				relay.Unsubscribe(relayPipe)
				return
			}

			// Kill the silence FFmpeg and start a new one with the relay pipe.
			if p.ff != nil {
				p.ff.Kill()
			}
			musicFF, err := startFFmpeg(stream.NewRelaySource(relayPipe), "music: "+musicName)
			if err != nil {
				pl.Printf("ffmpeg start (music): %v", err)
				relay.Unsubscribe(relayPipe)
				return
			}
			p.ff = musicFF
			p.relayMu.Lock()
			p.relay = relay
			p.relayPipe = relayPipe
			p.musicStream = musicName
			p.relayMu.Unlock()

			if m.classifier != nil {
				if u, err := url.Parse(streamURL); err == nil && u.Hostname() != "" {
					m.classifier.RegisterStream(u.Hostname(), musicName)
				}
			}
		}()

		wc.Wake() // trigger immediate weather refresh with fresh data
	}

	hub.OnIdle = func() {
		// Snapshot and nil out pipeline state under activeMu, then release
		// the lock BEFORE doing relay cleanup (which can block waiting for
		// the writer goroutine).  This ensures the next OnActive can
		// acquire activeMu promptly.
		p.activeMu.Lock()
		now := time.Now()
		p.lastSeen.Store(&now)

		if p.ff != nil {
			p.ff.Kill()
			p.ff = nil
			p.rnd.SetOutput(nil)
			pl.Printf("ffmpeg killed (no viewers)")
		}
		hub.CloseAllClients()
		hub.RequestActivation()

		p.relayMu.Lock()
		curRelay, curPipe := p.relay, p.relayPipe
		p.relay = nil
		p.relayPipe = nil
		p.relayMu.Unlock()
		p.activeMu.Unlock()

		// Relay cleanup runs outside activeMu — Unsubscribe may block
		// briefly while the writer goroutine drains its pipe.
		if curRelay != nil && curPipe != nil {
			curRelay.SetActive(curPipe, false)
			curRelay.Unsubscribe(curPipe)
		}
	}

	// Start initial FFmpeg with silence so the HLS segmenter can produce
	// segments from loading frames before any viewer connects. The first
	// OnActive reuses this process (see ffIsInitial) instead of restarting it,
	// so the first viewer starts receiving frames without extra startup delay.
	ff, err := startFFmpeg(stream.NewSilenceSource(), "initial silence")
	if err != nil {
		cancel()
		return nil, fmt.Errorf("ffmpeg for ZIP %s: %w", loc.ZipCode, err)
	}
	p.ff = ff
	p.ffIsInitial = true

	// Renderer writes frames to FFmpeg stdin.
	use24h := clockFormat != config.ClockFormat12h
	useMetric := units == config.UnitsMetric
	p.rnd = renderer.New(m.cfg.Width, m.cfg.Height, m.cfg.FrameRate, loc.ZipCode,
		m.store.SlideDuration,
		2*m.cfg.WeatherRefresh,
		wc, ff.Stdin(),
		func() bool { return hub.ClientCount() > 0 },
		m.store.Announcements, m.store.AnnouncementDuration, m.store.AnnouncementInterval,
		m.store.TriviaItems, m.store.TriviaDuration, m.store.TriviaInterval, m.store.TriviaRandomize,
		m.store.RealisticMoonIcons,
		m.store.FunSunIcons,
		use24h, useMetric,
		tzLoc)
	go func() {
		if err := p.rnd.Run(ctx); err != nil && ctx.Err() == nil {
			pl.Printf("renderer: %v", err)
		}
		// Renderer exited — either context was cancelled (app shutdown)
		// or an unexpected error.  Clean up FFmpeg and relay without
		// acquiring activeMu (OnActive/OnIdle may also be running).
		if p.ff != nil {
			p.ff.Kill()
		}
		p.relayMu.Lock()
		r, rp := p.relay, p.relayPipe
		p.relay = nil
		p.relayPipe = nil
		p.relayMu.Unlock()
		if r != nil && rp != nil {
			r.Unsubscribe(rp)
		}
	}()

	pl.Printf("started (%dx%d @ %dfps)", m.cfg.Width, m.cfg.Height, m.cfg.FrameRate)
	return p, nil
}

// pickStream selects a music stream URL for the pipeline. Returns empty
// strings if no stream is configured (local files or silence).
func (m *Manager) pickStream(p *Pipeline) (streamURL, musicName string) {
	if m.cfg.MusicStreamURL != "" {
		return m.cfg.MusicStreamURL, m.cfg.MusicStreamURL
	}
	entries := m.store.Streams()
	if len(entries) == 0 {
		return "", ""
	}
	entry := entries[rand.Intn(len(entries))]
	return entry.URL, entry.DisplayName()
}
