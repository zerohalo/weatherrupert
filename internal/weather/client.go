package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zerohalo/weatherrupert/internal/apiurl"
	"github.com/zerohalo/weatherrupert/internal/config"
)

// radarIEM is the Iowa Environmental Mesonet radar map endpoint.
// It renders NEXRAD composite reflectivity + county/state borders in a single PNG.
var radarIEM = apiurl.IEMRadar

const userAgent = "weatherrupert/1.0 (github.com/zerohalo/weatherrupert)"

// Client fetches and refreshes weather data from the NWS API.
// baseURL is typically "https://api.weather.gov" but can be overridden to a
// caching proxy (e.g., a WS4KP server-mode instance at "http://ws4kp:8080/api").
type Client struct {
	baseURL    string
	http       *http.Client
	lat, lon   float64
	locationMu sync.Mutex
	location   string
	gridID     string
	gridX      int
	gridY      int
	stationIDs []string // observation stations, nearest first
	data       atomic.Pointer[WeatherData]

	// Tide station state (populated at bootstrap, best-effort).
	tideStations  []TideStation
	nearestTide   *TideStation
	tideDistMiles float64

	// staticMap holds a pre-fetched basemap PNG used as a radar fallback.
	staticMap []byte

	// Radar & satellite imagery settings.
	frames int     // number of animation frames (default: 4)
	radius float64 // bounding box radius in miles (default: 120)

	// Satellite settings.
	getSatelliteProduct func() string // "IR" or "VIS"; called on each fetch

	// Solar weather state (fetched independently on a long interval).
	solarData atomic.Pointer[SolarData]

	// wake is signalled to trigger an immediate refresh (e.g. when a viewer connects).
	wake chan struct{}
}

// NewClient creates a new weather client. Bootstrap must be called before Run or Current.
func NewClient(baseURL string, lat, lon float64, location string, frames int, radius float64, getSatelliteProduct func() string, httpClient *http.Client) *Client {
	if frames <= 0 {
		frames = 4
	}
	if radius <= 0 {
		radius = 120.0
	}
	if getSatelliteProduct == nil {
		getSatelliteProduct = func() string { return config.SatelliteIR }
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		baseURL:             baseURL,
		lat:                 lat,
		lon:                 lon,
		location:            location,
		frames:              frames,
		radius:              radius,
		getSatelliteProduct: getSatelliteProduct,
		http:                httpClient,
		wake:                make(chan struct{}, 1),
	}
}

// Bootstrap resolves the NWS grid coordinates and observation station.
// Retries with exponential backoff until successful or ctx is cancelled.
func (c *Client) Bootstrap(ctx context.Context) error {
	backoff := 2 * time.Second
	maxBackoff := 30 * time.Second
	deadline := time.Now().Add(5 * time.Minute)

	for {
		err := c.bootstrap(ctx)
		if err == nil {
			log.Printf("weather: bootstrapped for %s (grid %s/%d,%d, %d stations)",
				c.location, c.gridID, c.gridX, c.gridY, len(c.stationIDs))
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("weather: bootstrap timed out after 5 minutes: %w", err)
		}

		log.Printf("weather: bootstrap failed (%v), retrying in %s", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Client) bootstrap(ctx context.Context) error {
	// Steps 3 & 4 only need lat/lon, so launch them concurrently with steps 1-2.
	var wg sync.WaitGroup

	// Step 3: discover NOAA tide stations (best-effort, non-fatal).
	wg.Add(1)
	go func() {
		defer wg.Done()
		tideCtx, tideCancel := context.WithTimeout(ctx, 15*time.Second)
		defer tideCancel()
		stations, err := fetchTideStations(tideCtx, c.http)
		if err != nil {
			log.Printf("weather: tide station fetch failed (non-fatal): %v", err)
			return
		}
		c.tideStations = stations
		nearest, dist := findNearestStation(stations, c.lat, c.lon, 100)
		if nearest != nil {
			c.nearestTide = nearest
			c.tideDistMiles = dist
			log.Printf("weather: nearest tide station: %s (%s) at %.1f mi",
				nearest.Name, nearest.ID, dist)
		} else {
			log.Printf("weather: no tide station within 100 miles — moon-only mode")
		}
	}()

	// Step 4: fetch a static basemap for radar fallback (best-effort, non-fatal).
	wg.Add(1)
	go func() {
		defer wg.Done()
		mapCtx, mapCancel := context.WithTimeout(ctx, 10*time.Second)
		defer mapCancel()
		smap, err := c.fetchStaticMap(mapCtx)
		if err != nil {
			log.Printf("weather: static map fetch failed (non-fatal): %v", err)
			return
		}
		c.staticMap = smap
		log.Printf("weather: fetched static basemap (%d bytes)", len(smap))
	}()

	// Step 1: resolve grid coordinates from lat/lon
	url := fmt.Sprintf("%s/points/%.4f,%.4f", c.baseURL, c.lat, c.lon)
	var pts PointsResponse
	if err := c.getJSON(ctx, url, &pts); err != nil {
		return fmt.Errorf("points: %w", err)
	}

	c.gridID = pts.Properties.GridID
	c.gridX = pts.Properties.GridX
	c.gridY = pts.Properties.GridY

	// Update location with city/state from NWS if available.
	if city := pts.Properties.RelativeLocation.Properties.City; city != "" {
		state := pts.Properties.RelativeLocation.Properties.State
		c.locationMu.Lock()
		c.location = fmt.Sprintf("%s, %s", city, state)
		c.locationMu.Unlock()
	}

	// Step 2: resolve the nearest observation station.
	// Use the URL from the points response but rewrite to our base URL if needed.
	stationsURL := pts.Properties.ObservationStations
	if stationsURL == "" {
		stationsURL = fmt.Sprintf("%s/gridpoints/%s/%d,%d/stations",
			c.baseURL, c.gridID, c.gridX, c.gridY)
	} else {
		stationsURL = rewriteToBase(c.baseURL, stationsURL)
	}

	stationIDs, err := c.resolveStations(ctx, stationsURL)
	if err != nil {
		return fmt.Errorf("station: %w", err)
	}
	c.stationIDs = stationIDs

	// Step 5: do an initial data fetch — wait for concurrent steps 3 & 4.
	wg.Wait()
	data, err := c.fetch(ctx)
	if err != nil {
		return fmt.Errorf("initial fetch: %w", err)
	}
	c.data.Store(data)
	return nil
}

// runSolarRefresh fetches solar data immediately then refreshes every hour.
// Runs until ctx is cancelled. Skips fetches when no viewers are connected.
func (c *Client) runSolarRefresh(ctx context.Context, hasClients func() bool) {
	doFetch := func() {
		log.Printf("weather: fetching solar data (images may take ~60s)...")
		solar := fetchSolar(ctx, c.http)
		if solar != nil {
			c.solarData.Store(solar)
			hasImages := solar.SunspotImage != nil || solar.CoronaImage != nil
			log.Printf("weather: solar data refreshed (images=%v, kp=%.1f, xray=%s)",
				hasImages, solar.KpIndex, solar.XRayFlux)
		} else {
			log.Printf("weather: solar data fetch failed entirely")
		}
	}

	// Initial fetch (always, regardless of viewers).
	doFetch()

	ticker := time.NewTicker(solarRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if hasClients != nil && !hasClients() {
				continue
			}
			doFetch()
		}
	}
}

// resolveStations returns up to maxStations station IDs sorted by proximity.
func (c *Client) resolveStations(ctx context.Context, stationsURL string) ([]string, error) {
	const maxStations = 5
	var resp StationsResponse
	if err := c.getJSON(ctx, stationsURL, &resp); err != nil {
		return nil, err
	}
	if len(resp.Features) == 0 {
		return nil, fmt.Errorf("no observation stations found")
	}
	ids := make([]string, 0, maxStations)
	for _, f := range resp.Features {
		ids = append(ids, f.Properties.StationIdentifier)
		if len(ids) >= maxStations {
			break
		}
	}
	return ids, nil
}

// Run starts the background weather refresh loop. Blocks until ctx is cancelled.
// If hasClients is non-nil, refreshes are skipped when no clients are connected.
// Call Wake() to trigger an immediate refresh (e.g. when a viewer connects).
func (c *Client) Run(ctx context.Context, interval time.Duration, hasClients func() bool) {
	// Start background solar refresh (skips fetches when no viewers connected).
	go c.runSolarRefresh(ctx, hasClients)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	idle := hasClients != nil && !hasClients()

	doFetch := func() {
		data, err := c.fetch(ctx)
		if err != nil {
			log.Printf("weather: refresh failed: %v", err)
			return
		}
		c.data.Store(data)
		tempLog := "n/a"
		if data.Current.TempF != nil {
			tempLog = fmt.Sprintf("%.1f°F", *data.Current.TempF)
		}
		log.Printf("weather: refreshed for %s (%s, %s)",
			data.Location, tempLog, data.Current.Description)
	}

	var lastFetch time.Time

	for {
		if idle {
			// No viewers — block until woken or cancelled.
			select {
			case <-ctx.Done():
				return
			case <-c.wake:
				idle = false
				log.Printf("weather: viewer connected, resuming refresh")
				doFetch()
				lastFetch = time.Now()
				// Drain any pending tick before resetting; in Go < 1.23
				// Reset does not clear the channel.
				select {
				case <-ticker.C:
				default:
				}
				ticker.Reset(interval)
			}
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-c.wake:
			// Skip if we fetched very recently (e.g. ticker and wake racing).
			if time.Since(lastFetch) < interval/2 {
				continue
			}
			doFetch()
			lastFetch = time.Now()
			ticker.Reset(interval)
		case <-ticker.C:
			if hasClients != nil && !hasClients() {
				idle = true
				log.Printf("weather: no viewers, pausing refresh")
				continue
			}
			doFetch()
			lastFetch = time.Now()
		}
	}
}

// Current returns the latest WeatherData. Returns nil before Bootstrap completes.
func (c *Client) Current() *WeatherData {
	return c.data.Load()
}

// StoreData stores weather data directly, for use in tests.
func (c *Client) StoreData(d *WeatherData) {
	c.data.Store(d)
}

// Wake triggers an immediate weather refresh on the next iteration of Run.
// Safe to call from any goroutine. Non-blocking; extra signals are coalesced.
func (c *Client) Wake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Location returns the resolved location string (e.g. "Denver, CO").
func (c *Client) Location() string {
	c.locationMu.Lock()
	defer c.locationMu.Unlock()
	return c.location
}

func (c *Client) fetch(ctx context.Context) (*WeatherData, error) {
	// Fetch daily forecast (3 attempts).
	forecastURL := fmt.Sprintf("%s/gridpoints/%s/%d,%d/forecast",
		c.baseURL, c.gridID, c.gridX, c.gridY)
	var dailyResp ForecastResponse
	if err := c.getJSONRetry(ctx, "forecast", forecastURL, &dailyResp, 3); err != nil {
		return nil, fmt.Errorf("forecast: %w", err)
	}

	// Fetch hourly forecast (3 attempts).
	hourlyURL := fmt.Sprintf("%s/gridpoints/%s/%d,%d/forecast/hourly",
		c.baseURL, c.gridID, c.gridX, c.gridY)
	var hourlyResp ForecastResponse
	if err := c.getJSONRetry(ctx, "hourly forecast", hourlyURL, &hourlyResp, 3); err != nil {
		return nil, fmt.Errorf("hourly forecast: %w", err)
	}

	// Fetch latest observation, trying stations in order until we get one
	// with a non-null temperature (NWS occasionally returns null for key fields).
	// Each station attempt retries up to 3 times for transient errors.
	var obsResp ObservationResponse
	var obsStation string
	for _, sid := range c.stationIDs {
		obsURL := fmt.Sprintf("%s/stations/%s/observations/latest",
			c.baseURL, sid)
		var resp ObservationResponse
		if err := c.getJSONRetry(ctx, "observation "+sid, obsURL, &resp, 3); err != nil {
			log.Printf("weather: station %s observation failed: %v", sid, err)
			continue
		}
		obsResp = resp
		obsStation = sid
		if resp.Properties.Temperature.Value.Value != nil {
			break // got a station with real temperature data
		}
		log.Printf("weather: station %s returned null temperature, trying next", sid)
	}
	if obsStation == "" {
		return nil, fmt.Errorf("observation: all %d stations failed", len(c.stationIDs))
	}

	// Limit to 12 hourly and 14 daily periods (14 = 7 days × day+night pairs → 6 cards).
	hourly := hourlyResp.Properties.Periods
	if len(hourly) > 12 {
		hourly = hourly[:12]
	}
	daily := dailyResp.Properties.Periods
	if len(daily) > 14 {
		daily = daily[:14]
	}

	// Fetch radar and satellite animation frames concurrently — best-effort, does not abort refresh.
	var frames, satFrames [][]byte
	var frameWg sync.WaitGroup
	frameWg.Add(2)
	go func() { defer frameWg.Done(); frames = c.fetchRadarFrames(ctx) }()
	go func() { defer frameWg.Done(); satFrames = c.fetchSatelliteFrames(ctx) }()
	frameWg.Wait()

	// Compute moon phase (pure function, no API call).
	moon := ComputeMoonPhase(time.Now())

	// Compute planet positions (pure function, no API call).
	planets := ComputePlanets(time.Now(), c.lat, c.lon)

	// Fetch tide predictions if a nearby station was found (best-effort).
	var tideData *TideData
	if c.nearestTide != nil {
		now := time.Now().Local()
		const tideRetries = 3
		var preds []TidePrediction
		var tideErr error
		for attempt := 1; attempt <= tideRetries; attempt++ {
			tideCtx, tideCancel := context.WithTimeout(ctx, 10*time.Second)
			preds, tideErr = fetchTidePredictions(tideCtx, c.http, c.nearestTide.ID, now)
			tideCancel()
			if tideErr == nil {
				break
			}
			if ctx.Err() != nil {
				break
			}
			if attempt < tideRetries {
				log.Printf("weather: tide predictions attempt %d/%d failed: %v, retrying", attempt, tideRetries, tideErr)
				time.Sleep(2 * time.Second)
			} else {
				log.Printf("weather: tide predictions failed after %d attempts (non-fatal): %v", tideRetries, tideErr)
			}
		}
		if tideErr == nil && len(preds) > 0 {
			tideData = &TideData{
				Station:       *c.nearestTide,
				DistanceMiles: c.tideDistMiles,
				Predictions:   preds,
			}
			// Fetch exact high/low tide times (best-effort, 3 attempts).
			var hilo []TideHiLo
			var hiloErr error
			for attempt := 1; attempt <= tideRetries; attempt++ {
				hiloCtx, hiloCancel := context.WithTimeout(ctx, 10*time.Second)
				hilo, hiloErr = fetchTideHiLo(hiloCtx, c.http, c.nearestTide.ID, now)
				hiloCancel()
				if hiloErr == nil {
					break
				}
				if ctx.Err() != nil {
					break
				}
				if attempt < tideRetries {
					log.Printf("weather: tide hilo attempt %d/%d failed: %v, retrying", attempt, tideRetries, hiloErr)
					time.Sleep(2 * time.Second)
				} else {
					log.Printf("weather: tide hilo failed after %d attempts (non-fatal): %v", tideRetries, hiloErr)
				}
			}
			if hiloErr == nil {
				tideData.HiLo = hilo
			}
		}
	}

	// Fetch active weather alerts (best-effort, non-fatal).
	alertCtx, alertCancel := context.WithTimeout(ctx, 10*time.Second)
	alerts := fetchAlerts(alertCtx, c.baseURL, c.lat, c.lon, c.http)
	alertCancel()

	// Fetch raw gridpoint data for precipitation totals (best-effort, non-fatal).
	precipMM, snowMM := c.fetchGridPointRaw(ctx)

	return &WeatherData{
		Location:        c.location,
		FetchedAt:       time.Now(),
		Current:         parseObservation(&obsResp),
		HourlyPeriods:   hourly,
		DailyPeriods:    daily,
		RadarFrames:     frames,
		SatelliteFrames: satFrames,
		StaticMap:       c.staticMap,
		MoonPhase:       moon,
		Planets:         planets,
		TideData:        tideData,
		Alerts:          alerts,
		Solar:           c.solarData.Load(),
		PrecipTotal24h:  precipMM,
		SnowTotal24h:    snowMM,
	}, nil
}

// fetchRadarFrames fetches 4 NEXRAD composite frames at hourly intervals going
// back 3 hours (oldest→newest), all in parallel. Each frame covers the same
// ~120-mile bounding box. Any frame that fails is omitted; the slide will
// animate whatever frames arrive. Uses IEM's ts= parameter (YYYYMMDDHHmm UTC)
// to request historical scans so animation is ready from the very first load.
func (c *Client) fetchRadarFrames(ctx context.Context) [][]byte {
	numFrames := c.frames

	now := time.Now().UTC()
	frames := make([][]byte, numFrames)

	var wg sync.WaitGroup
	for i := 0; i < numFrames; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			// i=0 → oldest, i=numFrames-1 → now (newest)
			offset := time.Duration(numFrames-1-i) * time.Hour
			t := now.Add(-offset).Round(5 * time.Minute)

			radarCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			png, err := c.fetchRadarAt(radarCtx, t)
			if err != nil {
				log.Printf("weather: radar frame %d (%s) failed: %v", i, t.Format("15:04Z"), err)
				return
			}
			frames[i] = png
		}()
	}
	wg.Wait()

	// Compact to a contiguous slice, preserving oldest→newest order.
	out := frames[:0]
	for _, f := range frames {
		if f != nil {
			out = append(out, f)
		}
	}
	return out
}

// fetchRadarAt fetches a single NEXRAD composite PNG from IEM for the given UTC time.
// imgW/imgH match the slide panel so no client-side scaling is needed.
func (c *Client) fetchRadarAt(ctx context.Context, t time.Time) ([]byte, error) {
	const (
		imgW        = 1280
		imgH        = 610
		milesPerDeg = 69.0
	)
	radiusMiles := c.radius

	latSpan := radiusMiles / milesPerDeg
	lonSpan := radiusMiles / (milesPerDeg * math.Cos(c.lat*math.Pi/180))

	q := url.Values{}
	q.Add("layers[]", "n0q")        // NEXRAD N0Q composite reflectivity (high-res)
	q.Add("layers[]", "uscounties") // US county boundaries
	q.Set("width", fmt.Sprintf("%d", imgW))
	q.Set("height", fmt.Sprintf("%d", imgH))
	q.Set("bbox", fmt.Sprintf("%.4f,%.4f,%.4f,%.4f",
		c.lon-lonSpan, c.lat-latSpan,
		c.lon+lonSpan, c.lat+latSpan))
	// ts format accepted by IEM radmap.php: YYYYMMDDHHmm (UTC, zero-padded)
	q.Set("ts", t.Format("200601021504"))

	return c.getImage(ctx, radarIEM+"?"+q.Encode())
}

// fetchStaticMap fetches a basemap PNG (county borders, no radar overlay) from IEM.
// Used as a fallback when radar frames are unavailable.
func (c *Client) fetchStaticMap(ctx context.Context) ([]byte, error) {
	const (
		imgW        = 1280
		imgH        = 610
		milesPerDeg = 69.0
	)
	radiusMiles := c.radius

	latSpan := radiusMiles / milesPerDeg
	lonSpan := radiusMiles / (milesPerDeg * math.Cos(c.lat*math.Pi/180))

	q := url.Values{}
	q.Add("layers[]", "uscounties")
	q.Set("width", fmt.Sprintf("%d", imgW))
	q.Set("height", fmt.Sprintf("%d", imgH))
	q.Set("bbox", fmt.Sprintf("%.4f,%.4f,%.4f,%.4f",
		c.lon-lonSpan, c.lat-latSpan,
		c.lon+lonSpan, c.lat+latSpan))

	return c.getImage(ctx, radarIEM+"?"+q.Encode())
}

// fetchSatelliteFrames fetches GOES visible satellite frames at hourly intervals
// going back (oldest→newest), all in parallel. Mirrors fetchRadarFrames but uses
// the GOES VIS layer instead of NEXRAD reflectivity.
func (c *Client) fetchSatelliteFrames(ctx context.Context) [][]byte {
	numFrames := c.frames

	now := time.Now().UTC()
	frames := make([][]byte, numFrames)

	var wg sync.WaitGroup
	for i := 0; i < numFrames; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			offset := time.Duration(numFrames-1-i) * time.Hour
			t := now.Add(-offset).Round(5 * time.Minute)

			satCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			png, err := c.fetchSatelliteAt(satCtx, t)
			if err != nil {
				log.Printf("weather: satellite frame %d (%s) failed: %v", i, t.Format("15:04Z"), err)
				return
			}
			frames[i] = png
		}()
	}
	wg.Wait()

	out := frames[:0]
	for _, f := range frames {
		if f != nil {
			out = append(out, f)
		}
	}
	return out
}

// fetchSatelliteAt fetches a single GOES visible satellite PNG from IEM for the given UTC time.
func (c *Client) fetchSatelliteAt(ctx context.Context, t time.Time) ([]byte, error) {
	const (
		imgW        = 1280
		imgH        = 610
		milesPerDeg = 69.0
	)
	radiusMiles := c.radius

	latSpan := radiusMiles / milesPerDeg
	lonSpan := radiusMiles / (milesPerDeg * math.Cos(c.lat*math.Pi/180))

	q := url.Values{}
	q.Add("layers[]", "goes")
	q.Set("goes_product", c.getSatelliteProduct())
	q.Add("layers[]", "uscounties")
	q.Set("width", fmt.Sprintf("%d", imgW))
	q.Set("height", fmt.Sprintf("%d", imgH))
	q.Set("bbox", fmt.Sprintf("%.4f,%.4f,%.4f,%.4f",
		c.lon-lonSpan, c.lat-latSpan,
		c.lon+lonSpan, c.lat+latSpan))
	q.Set("ts", t.Format("200601021504"))

	return c.getImage(ctx, radarIEM+"?"+q.Encode())
}

// getImage fetches a URL and returns the raw response bytes.
func (c *Client) getImage(ctx context.Context, imageURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, imageURL)
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) getJSON(ctx context.Context, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/geo+json,application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	return json.NewDecoder(resp.Body).Decode(dst)
}

// getJSONRetry calls getJSON up to maxAttempts times with a 2-second delay
// between attempts. It gives up immediately if the parent context is cancelled.
func (c *Client) getJSONRetry(ctx context.Context, label string, url string, dst any, maxAttempts int) error {
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = c.getJSON(ctx, url, dst)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if attempt < maxAttempts {
			log.Printf("weather: %s attempt %d/%d failed: %v, retrying", label, attempt, maxAttempts, err)
			time.Sleep(2 * time.Second)
		}
	}
	return err
}

// fetchGridPointRaw fetches the raw gridpoint data and returns 24h precipitation
// totals (liquid precip in mm, snowfall in mm). Best-effort: returns (0, 0) on error.
func (c *Client) fetchGridPointRaw(ctx context.Context) (precipMM, snowMM float64) {
	rawCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rawURL := fmt.Sprintf("%s/gridpoints/%s/%d,%d",
		c.baseURL, c.gridID, c.gridX, c.gridY)

	var raw gridPointRaw
	if err := c.getJSON(rawCtx, rawURL, &raw); err != nil {
		log.Printf("weather: gridpoint raw fetch failed (non-fatal): %v", err)
		return 0, 0
	}

	now := time.Now()
	end := now.Add(24 * time.Hour)
	precipMM = sumGridSeries(raw.Properties.QuantitativePrecipitation, now, end)
	snowMM = sumGridSeries(raw.Properties.SnowfallAmount, now, end)
	return precipMM, snowMM
}

// parseValidTime splits an ISO 8601 interval like "2026-02-28T16:00:00+00:00/PT1H"
// into a start time and duration.
func parseValidTime(s string) (time.Time, time.Duration, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, fmt.Errorf("invalid validTime: %q", s)
	}
	start, err := time.Parse(time.RFC3339, parts[0])
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("invalid start time: %w", err)
	}
	dur, err := parseISO8601Duration(parts[1])
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("invalid duration: %w", err)
	}
	return start, dur, nil
}

// parseISO8601Duration parses a subset of ISO 8601 durations (PT#H, PT#M, PT#H#M, P#D, etc.).
func parseISO8601Duration(s string) (time.Duration, error) {
	if !strings.HasPrefix(s, "P") {
		return 0, fmt.Errorf("duration must start with P: %q", s)
	}
	s = s[1:] // strip "P"
	var total time.Duration

	// Handle days before "T" marker.
	if idx := strings.Index(s, "D"); idx >= 0 {
		var days int
		if _, err := fmt.Sscanf(s[:idx], "%d", &days); err != nil {
			return 0, fmt.Errorf("bad days in duration: %w", err)
		}
		total += time.Duration(days) * 24 * time.Hour
		s = s[idx+1:]
	}

	if strings.HasPrefix(s, "T") {
		s = s[1:]
	}
	if s == "" {
		return total, nil
	}

	// Parse hours.
	if idx := strings.Index(s, "H"); idx >= 0 {
		var hours int
		if _, err := fmt.Sscanf(s[:idx], "%d", &hours); err != nil {
			return 0, fmt.Errorf("bad hours in duration: %w", err)
		}
		total += time.Duration(hours) * time.Hour
		s = s[idx+1:]
	}
	// Parse minutes.
	if idx := strings.Index(s, "M"); idx >= 0 {
		var mins int
		if _, err := fmt.Sscanf(s[:idx], "%d", &mins); err != nil {
			return 0, fmt.Errorf("bad minutes in duration: %w", err)
		}
		total += time.Duration(mins) * time.Minute
		s = s[idx+1:]
	}
	return total, nil
}

// sumGridSeries sums all values in the series whose interval overlaps [from, to).
func sumGridSeries(series gridTimeSeries, from, to time.Time) float64 {
	var total float64
	for _, v := range series.Values {
		if v.Value == nil {
			continue
		}
		start, dur, err := parseValidTime(v.ValidTime)
		if err != nil {
			continue
		}
		end := start.Add(dur)
		// Check if the interval overlaps [from, to).
		if end.After(from) && start.Before(to) {
			total += *v.Value
		}
	}
	return total
}

// rewriteToBase rewrites an absolute NWS URL to use our configured base URL.
// This allows the client to work through a caching proxy if configured.
// Example: "https://api.weather.gov/gridpoints/..." → "{baseURL}/gridpoints/..."
func rewriteToBase(baseURL, nwsURL string) string {
	nwsHost := apiurl.DefaultNWSBase
	if len(nwsURL) >= len(nwsHost) && nwsURL[:len(nwsHost)] == nwsHost {
		return baseURL + nwsURL[len(nwsHost):]
	}
	return nwsURL
}
