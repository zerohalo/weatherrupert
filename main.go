package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os/signal"
	"syscall"
	"time"

	"github.com/zerohalo/weatherrupert/internal/admin"
	"github.com/zerohalo/weatherrupert/internal/announcements"
	"github.com/zerohalo/weatherrupert/internal/apistats"
	"github.com/zerohalo/weatherrupert/internal/apiurl"
	"github.com/zerohalo/weatherrupert/internal/config"
	"github.com/zerohalo/weatherrupert/internal/geo"
	"github.com/zerohalo/weatherrupert/internal/guide"
	"github.com/zerohalo/weatherrupert/internal/renderer"
	"github.com/zerohalo/weatherrupert/internal/stream"
	"github.com/zerohalo/weatherrupert/internal/sysstat"
	"github.com/zerohalo/weatherrupert/internal/trivia"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Default SomaFM streams — pre-populate the admin settings panel.
	// The manager picks one at random per pipeline unless overridden by the admin UI.
	defaultStreams := []admin.StreamEntry{
		{Name: "Secret Agent", URL: "https://ice1.somafm.com/secretagent-128-mp3"},
		{Name: "Illinois Street Lounge", URL: "https://ice1.somafm.com/illstreet-128-mp3"},
	}

	// Local-file fallback used when no stream URLs are configured in the admin
	// panel and MUSIC_STREAM_URL is not set.
	fallbackMusic, err := stream.ScanMusicDir(cfg.MusicDir)
	if err != nil {
		log.Printf("music: %v (using silence)", err)
		fallbackMusic = &stream.MusicSource{}
	}

	msgs := announcements.Load(cfg.AnnouncementsPath)
	triviaItems := trivia.Load(cfg.TriviaPath)

	store := admin.NewStore(cfg.AdminDataPath, msgs, triviaItems,
		cfg.SlideDuration, cfg.AnnouncementDuration, cfg.TriviaDuration,
		cfg.AnnouncementInterval, cfg.TriviaInterval,
		defaultStreams,
		cfg.TriviaAPI, cfg.TriviaAPIAmount, cfg.TriviaAPICategory,
		cfg.TriviaAPIDifficulty, cfg.TriviaAPIRefresh, cfg.TriviaBuiltin)

	// Derive the NWS API hostname so a custom proxy URL is classified correctly.
	nwsHost := "api.weather.gov"
	if u, err := url.Parse(cfg.WeatherAPIURL); err == nil && u.Hostname() != "" {
		nwsHost = u.Hostname()
	}

	classifier := apiurl.NewClassifier(nwsHost)
	apiTracker := apistats.New(classifier.Classify)

	trackedClient := &http.Client{
		Timeout:   15 * time.Second,
		Transport: apiTracker.Transport(http.DefaultTransport),
	}
	streamTrackedClient := &http.Client{
		Transport: apiTracker.Transport(http.DefaultTransport),
	}

	mgr := NewManager(ctx, cfg, fallbackMusic, store, trackedClient, streamTrackedClient, classifier)

	if store.TriviaAPI() {
		go func() {
			// Fetch immediately on startup so trivia is ready for the first viewer.
			fetchTrivia(store, trackedClient)

			// If a refresh interval is configured, re-fetch periodically
			// but only when viewers are connected.
			interval := store.TriviaAPIRefresh()
			if interval <= 0 {
				return
			}
			lastFetch := time.Now()
			const pollInterval = 5 * time.Second
			ticker := time.NewTicker(pollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if !mgr.HasViewers() {
						continue
					}
					if time.Since(lastFetch) >= interval {
						fetchTrivia(store, trackedClient)
						lastFetch = time.Now()
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	store.SetPipelineSource(mgr.ActivePipelines)
	store.SetAPIStatsSource(apiTracker.Stats)

	cpuSampler := sysstat.NewCPUSampler()
	defer cpuSampler.Stop()
	store.SetSystemStatsSource(func() (loadAvg [3]float64, cpuPct float64) {
		l1, l5, l15, _ := sysstat.LoadAvg()
		return [3]float64{l1, l5, l15}, cpuSampler.Usage()
	})

	favicon, err := renderer.RenderFavicon()
	if err != nil {
		log.Fatalf("favicon: %v", err)
	}

	mux := http.NewServeMux()
	store.RegisterRoutes(mux)

	// GET /favicon.ico
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(favicon)
	})

	// clockParam returns the clock format from a request's ?clock= parameter,
	// falling back to the admin-configured default.
	clockParam := func(r *http.Request) string {
		c := r.URL.Query().Get("clock")
		if c == config.ClockFormat12h || c == config.ClockFormat24h {
			return c
		}
		return store.ClockFormat()
	}

	// unitsParam returns the unit system from a request's ?units= parameter,
	// falling back to the admin-configured default.
	unitsParam := func(r *http.Request) string {
		u := r.URL.Query().Get("units")
		if u == config.UnitsImperial || u == config.UnitsMetric {
			return u
		}
		return store.UnitSystem()
	}

	// GET /playlist.m3u?zip=90210[&clock=12|24]
	mux.HandleFunc("GET /playlist.m3u", func(w http.ResponseWriter, r *http.Request) {
		zip := r.URL.Query().Get("zip")
		if zip == "" {
			http.Error(w, "missing required query parameter: zip", http.StatusBadRequest)
			return
		}
		loc, err := geo.Lookup(zip)
		if err != nil {
			http.Error(w, fmt.Sprintf("unknown ZIP code %q", zip), http.StatusBadRequest)
			return
		}
		clock := clockParam(r)
		units := unitsParam(r)

		location := fmt.Sprintf("%s, %s", loc.City, loc.State)

		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		host := r.Host
		if host == "" {
			host = fmt.Sprintf("localhost:%d", cfg.Port)
		}
		channelID := "weather-" + loc.ZipCode
		var streamURL string
		if r.URL.Query().Get("format") == "hls" {
			streamURL = fmt.Sprintf("%s://%s/live.m3u8?zip=%s&clock=%s&units=%s", scheme, host, loc.ZipCode, clock, units)
		} else {
			streamURL = fmt.Sprintf("%s://%s/stream?zip=%s&clock=%s&units=%s", scheme, host, loc.ZipCode, clock, units)
		}
		baseURL := fmt.Sprintf("%s://%s", scheme, host)
		w.Header().Set("Content-Type", "audio/x-mpegurl")
		fmt.Fprint(w, guide.M3U(cfg.ChannelNumber, channelID, location, streamURL, baseURL, loc.ZipCode, clock, units))
	})

	// GET /guide.xml?zip=90210[&clock=12|24]
	mux.HandleFunc("GET /guide.xml", func(w http.ResponseWriter, r *http.Request) {
		zip := r.URL.Query().Get("zip")
		if zip == "" {
			http.Error(w, "missing required query parameter: zip", http.StatusBadRequest)
			return
		}
		loc, err := geo.Lookup(zip)
		if err != nil {
			http.Error(w, fmt.Sprintf("unknown ZIP code %q", zip), http.StatusBadRequest)
			return
		}
		location := fmt.Sprintf("%s, %s", loc.City, loc.State)
		channelID := "weather-" + loc.ZipCode
		data, err := guide.XMLTV(channelID, location, loc.ZipCode)
		if err != nil {
			http.Error(w, "guide generation error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Write(data)
	})

	// GET /live.m3u8?zip=90210[&clock=12|24][&units=imperial|metric]
	mux.HandleFunc("GET /live.m3u8", func(w http.ResponseWriter, r *http.Request) {
		zip := r.URL.Query().Get("zip")
		if zip == "" {
			http.Error(w, "missing required query parameter: zip", http.StatusBadRequest)
			return
		}
		clock := clockParam(r)
		units := unitsParam(r)
		p, err := mgr.Get(zip, clock, units)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p.seg.ServePlaylist(w, r)
	})

	// GET /segment?zip=90210&clock=12&units=imperial&seq=N
	mux.HandleFunc("GET /segment", func(w http.ResponseWriter, r *http.Request) {
		zip := r.URL.Query().Get("zip")
		if zip == "" {
			http.Error(w, "missing required query parameter: zip", http.StatusBadRequest)
			return
		}
		clock := clockParam(r)
		units := unitsParam(r)
		p, err := mgr.Get(zip, clock, units)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p.seg.ServeSegment(w, r)
	})

	// GET /stream?zip=90210[&clock=12|24]
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
		zip := r.URL.Query().Get("zip")
		if zip == "" {
			http.Error(w, "missing required query parameter: zip", http.StatusBadRequest)
			return
		}
		clock := clockParam(r)
		units := unitsParam(r)
		p, err := mgr.Get(zip, clock, units)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p.hub.ServeHTTP(w, r)
	})

	// GET /preview?zip=90210[&clock=12|24][&units=imperial|metric]
	mux.HandleFunc("GET /preview", func(w http.ResponseWriter, r *http.Request) {
		zip := r.URL.Query().Get("zip")
		if zip == "" {
			http.Error(w, "missing required query parameter: zip", http.StatusBadRequest)
			return
		}
		clock := clockParam(r)
		units := unitsParam(r)

		// Try existing pipeline first (no side effects).
		var pngData []byte
		if p := mgr.Peek(zip, clock, units); p != nil {
			var err error
			pngData, err = p.rnd.RenderPreview()
			if err != nil {
				http.Error(w, "preview render error", http.StatusInternalServerError)
				return
			}
			// Store at manager level so it survives even without a pipeline.
			mgr.StoreCachedPreview(zip, pngData)
		} else if cached := mgr.CachedPreview(zip); cached != nil {
			// No pipeline running but we have a cached preview from a previous session.
			pngData = cached
		} else {
			// No pipeline and no cache — spin one up to get a preview.
			p, err := mgr.Get(zip, clock, units)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			pngData, err = p.rnd.RenderPreview()
			if err != nil {
				http.Error(w, "preview render error", http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Write(pngData)
	})

	// GET /health
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // no timeout for streaming connections
	}

	go func() {
		log.Printf("Listening on :%d — usage: /playlist.m3u?zip=XXXXX", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	log.Println("Shutdown complete")
}

func fetchTrivia(store *admin.Store, httpClient *http.Client) {
	items, err := trivia.FetchFromAPI(store.TriviaAPIOptions(), httpClient)
	if err != nil {
		log.Printf("trivia: API fetch failed: %v", err)
		return
	}
	store.SetAPITrivia(items)
	log.Printf("trivia: fetched %d items from Open Trivia Database", len(items))
}
