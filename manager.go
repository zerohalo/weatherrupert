package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zerohalo/weatherrupert/internal/admin"
	"github.com/zerohalo/weatherrupert/internal/apiurl"
	"github.com/zerohalo/weatherrupert/internal/config"
	"github.com/zerohalo/weatherrupert/internal/geo"
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
	relays       map[string]*stream.MusicRelay // keyed by stream URL
	cfg          *config.Config
	music        *stream.MusicSource
	store        *admin.Store
	rootCtx      context.Context
	httpClient   *http.Client       // tracked HTTP client for weather APIs
	streamClient *http.Client       // tracked HTTP client for music streams (no timeout)
	classifier   *apiurl.Classifier // hostname→label mapper (registers stream names)
}

// NewManager creates a Manager. rootCtx should be the application shutdown context.
func NewManager(rootCtx context.Context, cfg *config.Config, music *stream.MusicSource, store *admin.Store, httpClient, streamClient *http.Client, classifier *apiurl.Classifier) *Manager {
	return &Manager{
		pipelines:    make(map[string]*Pipeline),
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
		hlsActive := p.seg.ClientCount()
		hubClients := p.hub.ClientCount()
		totalViewers := hubClients - hlsActive + hlsActive // direct + HLS
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
		diag := p.seg.Diagnostics()
		hubDiag := p.hub.Diagnostics()
		infos = append(infos, admin.PipelineInfo{
			ZIP:              p.zip,
			Location:         p.wc.Location(),
			ClockFormat:      p.clockFormat,
			Units:            p.units,
			Alerts:           alertCount,
			Viewers:          hubClients - hlsActive,
			Views:            p.hub.TotalViews() - p.seg.HubSubscriptions(),
			HLSViewers:       hlsActive,
			HLSViews:         p.seg.TotalViews(),
			MusicStream:      musicStream,
			ViewTime:         p.hub.TotalViewTime() - p.seg.HubSubscriptionTime(),
			HLSViewTime:      p.seg.TotalViewTime(),
			LastSeen:         lastSeen,
			StreamURL:        p.streamURL,
			SlowFrames:       p.rnd.SlowFrames(),
			FFmpegWarns:      p.ff.Warnings(),
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
func (m *Manager) Lookup(zip, clockFormat, units string) *Pipeline {
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
	key := loc.ZipCode + "#" + clockFormat + "#" + units

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pipelines[key]
}

// Get returns the pipeline for the given ZIP code and clock format, creating it
// on first call. Subsequent calls for the same (ZIP, clockFormat) pair return
// the cached pipeline immediately. clockFormat must be "12" or "24"; any other
// value is normalised to "24". An error is returned only if the ZIP is invalid.
func (m *Manager) Get(zip, clockFormat, units string) (*Pipeline, error) {
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

	key := loc.ZipCode + "#" + clockFormat + "#" + units

	m.mu.Lock()
	defer m.mu.Unlock()

	if p, ok := m.pipelines[key]; ok {
		return p, nil
	}

	p, err := m.start(loc, clockFormat, units)
	if err != nil {
		return nil, err
	}
	m.pipelines[key] = p
	return p, nil
}

// Peek returns an existing pipeline for the given ZIP/clock/units without
// creating one. Returns nil if no pipeline is running for that combination.
func (m *Manager) Peek(zip, clockFormat, units string) *Pipeline {
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
	key := loc.ZipCode + "#" + clockFormat + "#" + units

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pipelines[key]
}

// start launches all goroutines for a new pipeline. The pipeline is returned
// immediately; weather bootstrapping runs in the background so the stream
// begins serving a "Loading..." slide right away. clockFormat is "12" or "24".
func (m *Manager) start(loc geo.Location, clockFormat, units string) (*Pipeline, error) {
	cityLabel := fmt.Sprintf("%s, %s", loc.City, loc.State)
	wc := weather.NewClient(m.cfg.WeatherAPIURL, loc.Lat, loc.Lon, cityLabel, m.cfg.Frames, m.cfg.Radius, m.store.SatelliteProduct, m.httpClient)

	// Resolve the music source for this pipeline:
	//   1. MUSIC_STREAM_URL env var (single-URL pin) takes priority.
	//   2. Admin-configured stream URL list — one is chosen at random.
	//   3. Fallback to local files or silence (m.music).
	//
	// Stream URLs use a shared MusicRelay so that multiple pipelines share
	// a single HTTP connection instead of each opening their own.
	music := m.music
	musicName := "Local files"
	var streamURL string
	if m.cfg.MusicStreamURL != "" {
		streamURL = m.cfg.MusicStreamURL
		musicName = m.cfg.MusicStreamURL
	} else if entries := m.store.Streams(); len(entries) > 0 {
		entry := entries[rand.Intn(len(entries))]
		streamURL = entry.URL
		musicName = entry.DisplayName()
	}

	// Register the stream hostname→name so API stats show the stream name.
	if streamURL != "" && m.classifier != nil {
		if u, err := url.Parse(streamURL); err == nil && u.Hostname() != "" {
			m.classifier.RegisterStream(u.Hostname(), musicName)
		}
	}

	var relayPipe *os.File
	var relay *stream.MusicRelay
	if streamURL != "" {
		relay = m.relays[streamURL]
		if relay == nil {
			relay = stream.NewMusicRelay(streamURL, m.streamClient)
			m.relays[streamURL] = relay
		}
		relayPipe = relay.Subscribe()
		if relayPipe != nil {
			music = stream.NewRelaySource(relayPipe)
			log.Printf("pipeline %s: music via shared relay for %s", loc.ZipCode, streamURL)
		} else {
			// Pipe creation failed — fall back to direct connection.
			log.Printf("pipeline %s: relay pipe failed, using direct connection for %s", loc.ZipCode, streamURL)
			music = stream.NewStreamSource(streamURL)
			relay = nil
		}
	}

	ff, err := stream.Start(m.cfg.Width, m.cfg.Height, m.cfg.FrameRate, music, loc.ZipCode, m.cfg.VideoMaxRate)
	if err != nil {
		if relayPipe != nil {
			relay.Unsubscribe(relayPipe)
		}
		return nil, fmt.Errorf("ffmpeg for ZIP %s: %w", loc.ZipCode, err)
	}

	// Start FFmpeg suspended — it will be resumed by the hub's OnActive
	// callback when the first viewer connects. This prevents the music stream
	// from consuming bandwidth while nobody is watching.
	ff.Suspend()

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
		musicStream:   musicName,
		streamURL:     reqURL,
		wc:            wc,
		hub:           hub,
		seg:           seg,
		ff:            ff,
		cancel:        cancel,
		locationReady: locationReady,
		relayPipe:     relayPipe,
		relay:         relay,
	}

	// Bootstrap + refresh loop (background — stream shows loading slide until ready).
	// locationReady is closed on exit (success or failure) so Location() never hangs.
	go func() {
		defer close(locationReady)
		if err := wc.Bootstrap(ctx); err != nil {
			if ctx.Err() == nil {
				log.Printf("pipeline %s: bootstrap failed: %v", loc.ZipCode, err)
			}
			return
		}
		log.Printf("pipeline %s: weather ready (%s)", loc.ZipCode, wc.Location())
		go wc.Run(ctx, m.cfg.WeatherRefresh, func() bool { return hub.ClientCount() > 0 })
	}()

	// Suspend FFmpeg when no viewers are connected to stop music stream
	// bandwidth and CPU usage. Resume when the first viewer connects.
	hub.OnActive = func() {
		// Serialize with OnIdle and shutdown cleanup to prevent races
		// on FFmpeg state and relay active counts.
		p.activeMu.Lock()
		defer p.activeMu.Unlock()

		// Rotate to a random music stream on each reconnection (only when
		// using admin-configured streams, not a pinned MUSIC_STREAM_URL).
		if m.cfg.MusicStreamURL == "" && relayPipe != nil {
			m.rotateRelay(p, ctx)
		}

		// Tell the relay this pipeline has viewers so it stays connected.
		p.relayMu.Lock()
		curRelay, curPipe := p.relay, p.relayPipe
		p.relayMu.Unlock()
		if curRelay != nil && curPipe != nil {
			curRelay.SetActive(curPipe, true)
			// Block until relay has an active HTTP connection so ffmpeg
			// doesn't produce video-only output during the audio gap.
			waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
			defer waitCancel()
			if err := curRelay.WaitConnected(waitCtx); err != nil {
				log.Printf("pipeline %s: music relay wait: %v (resuming without audio)", loc.ZipCode, err)
			}
			// Drain stale audio from the OS pipe kernel buffer while
			// ffmpeg is still frozen so it starts with fresh audio.
			curRelay.DrainPipe(curPipe)
		}
		// Reset the flush window so it starts from the actual resume
		// moment, not the earlier Subscribe call which may have been
		// seconds ago while waiting for the relay to connect.
		hub.ResetFlushWindow()
		if err := ff.Resume(); err != nil {
			log.Printf("pipeline %s: ffmpeg resume: %v", loc.ZipCode, err)
		} else {
			log.Printf("pipeline %s: ffmpeg resumed (viewer connected)", loc.ZipCode)
		}
		wc.Wake() // trigger immediate weather refresh with fresh data
	}
	hub.OnIdle = func() {
		p.activeMu.Lock()
		defer p.activeMu.Unlock()

		now := time.Now()
		p.lastSeen.Store(&now)
		// Stop audio flow BEFORE suspending FFmpeg so the relay doesn't
		// keep pumping data into the pipe while FFmpeg is still reading.
		// This minimizes stale audio that accumulates in FFmpeg's internal
		// thread queue before the freeze.
		p.relayMu.Lock()
		curRelay, curPipe := p.relay, p.relayPipe
		p.relayMu.Unlock()
		if curRelay != nil && curPipe != nil {
			curRelay.SetActive(curPipe, false)
		}
		if err := ff.Suspend(); err != nil {
			log.Printf("pipeline %s: ffmpeg suspend: %v", loc.ZipCode, err)
		} else {
			log.Printf("pipeline %s: ffmpeg suspended (no viewers)", loc.ZipCode)
		}
	}

	// Broadcast hub reads FFmpeg stdout.
	go hub.Run(ff.Stdout())

	// Renderer writes frames to FFmpeg stdin.
	// Pass hub.ClientCount so the renderer can skip frames when nobody is watching.
	// Store methods are passed as closures so that admin changes take effect live.
	use24h := clockFormat != config.ClockFormat12h
	useMetric := units == config.UnitsMetric
	p.rnd = renderer.New(m.cfg.Width, m.cfg.Height, m.cfg.FrameRate, loc.ZipCode,
		m.store.SlideDuration,
		wc, ff.Stdin(),
		func() bool { return hub.ClientCount() > 0 },
		m.store.Announcements, m.store.AnnouncementDuration, m.store.AnnouncementInterval,
		m.store.TriviaItems, m.store.TriviaDuration, m.store.TriviaInterval, m.store.TriviaRandomize,
		m.store.PlanetLiveAlways,
		use24h, useMetric)
	go func() {
		if err := p.rnd.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("pipeline %s: renderer: %v", loc.ZipCode, err)
		}
		ff.Stdin().Close()
		ff.Wait()
		// Serialize with OnActive/OnIdle so we read the final relay
		// after any in-flight rotation has completed.
		p.activeMu.Lock()
		p.relayMu.Lock()
		r, rp := p.relay, p.relayPipe
		p.relayMu.Unlock()
		p.activeMu.Unlock()
		// Clean up relay subscription so the writer goroutine and pipe FDs are released.
		if r != nil && rp != nil {
			r.Unsubscribe(rp)
		}
	}()

	log.Printf("pipeline %s: started (%dx%d @ %dfps)", loc.ZipCode, m.cfg.Width, m.cfg.Height, m.cfg.FrameRate)
	return p, nil
}

// rotateRelay picks a new random music stream and switches the pipeline's
// relay subscription.  Called from OnActive so each viewer session gets a
// different stream.  No-op when fewer than 2 admin streams are configured.
func (m *Manager) rotateRelay(p *Pipeline, ctx context.Context) {
	entries := m.store.Streams()
	if len(entries) < 2 {
		return // nothing to rotate
	}

	entry := entries[rand.Intn(len(entries))]
	newURL := entry.URL
	newName := entry.DisplayName()

	p.relayMu.Lock()
	oldRelay := p.relay
	oldPipe := p.relayPipe
	p.relayMu.Unlock()

	if oldRelay == nil || oldPipe == nil {
		return
	}

	// Get or create the relay for the new stream URL.
	m.mu.Lock()
	newRelay := m.relays[newURL]
	if newRelay == nil {
		newRelay = stream.NewMusicRelay(newURL, m.streamClient)
		m.relays[newURL] = newRelay
	}
	m.mu.Unlock()

	// Skip the detach/attach cycle if we picked the same relay
	// (avoids a brief audio gap for no benefit).
	if newRelay == oldRelay {
		return
	}

	// Detach the pipe from the old relay (stops its writer goroutine
	// without closing the pipe FDs that FFmpeg is reading from).
	pw := oldRelay.DetachPipe(oldPipe)
	if pw == nil {
		return
	}

	// Attach the existing pipe to the new relay.
	newRelay.AttachPipe(oldPipe, pw)

	p.relayMu.Lock()
	p.relay = newRelay
	p.musicStream = newName
	p.relayMu.Unlock()

	// Register the stream hostname so API stats show a friendly name.
	if m.classifier != nil {
		if u, err := url.Parse(newURL); err == nil && u.Hostname() != "" {
			m.classifier.RegisterStream(u.Hostname(), newName)
		}
	}

	log.Printf("pipeline %s: rotated music to %s", p.zip, newName)
}
