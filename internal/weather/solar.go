package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zerohalo/weatherrupert/internal/apiurl"
)

// Solar disk image URLs and NOAA SWPC API URLs are defined in apiurl package.
var (
	sunspotURLs = apiurl.SunspotURLs
	coronaURLs  = apiurl.CoronaURLs
)

var (
	noaaScalesURL    = apiurl.NOAAScales
	noaaXRayFlareURL = apiurl.NOAAXRayFlare
	noaaKpIndexURL   = apiurl.NOAAKpIndex
	noaaSolarWindURL = apiurl.NOAASolarWind
)

// solarImageTimeout is the per-attempt timeout for SDO image downloads.
// These are slow servers so we allow a generous window.
const solarImageTimeout = 60 * time.Second

// solarImageRetries is the maximum number of attempts per image fetch.
const solarImageRetries = 3

// solarRefreshInterval is how often the background solar goroutine refreshes.
const solarRefreshInterval = 1 * time.Hour

// fetchSolar fetches solar activity data from NOAA SWPC and NASA SDO.
// All sources are fetched in parallel. Image fetches use a 1-minute timeout
// with up to 3 retries. Returns nil only if every source fails.
func fetchSolar(ctx context.Context, httpClient *http.Client) *SolarData {
	sd := &SolarData{FetchedAt: time.Now()}
	var mu sync.Mutex
	var wg sync.WaitGroup
	anySuccess := false

	// fetchImageWithFallback tries each URL in order, with retries per URL.
	fetchImageWithFallback := func(urls []string) ([]byte, error) {
		var lastErr error
		for _, url := range urls {
			for attempt := 1; attempt <= solarImageRetries; attempt++ {
				fctx, cancel := context.WithTimeout(ctx, solarImageTimeout)
				data, err := fetchBytesCtx(fctx, httpClient, url)
				cancel()
				if err == nil {
					return data, nil
				}
				lastErr = err
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if attempt < solarImageRetries {
					log.Printf("weather: solar image %s attempt %d/%d failed: %v, retrying",
						url, attempt, solarImageRetries, err)
					time.Sleep(2 * time.Second)
				}
			}
			log.Printf("weather: solar image %s failed after %d attempts, trying next source", url, solarImageRetries)
		}
		return nil, lastErr
	}

	// fetchJSON is a helper for the fast NOAA JSON APIs (10s timeout, no retry).
	fetchJSON := func(url string, dst any) error {
		fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return fetchJSONCtx(fctx, httpClient, url, dst)
	}

	// 1. Sunspot image (tries SDAC mirror first, then SDO direct)
	wg.Add(1)
	go func() {
		defer wg.Done()
		data, err := fetchImageWithFallback(sunspotURLs)
		if err != nil {
			log.Printf("weather: solar sunspot image failed (non-fatal): %v", err)
			return
		}
		mu.Lock()
		sd.SunspotImage = data
		anySuccess = true
		mu.Unlock()
	}()

	// 2. Corona image (tries SDAC mirror first, then SDO direct)
	wg.Add(1)
	go func() {
		defer wg.Done()
		data, err := fetchImageWithFallback(coronaURLs)
		if err != nil {
			log.Printf("weather: solar corona image failed (non-fatal): %v", err)
			return
		}
		mu.Lock()
		sd.CoronaImage = data
		anySuccess = true
		mu.Unlock()
	}()

	// 3. NOAA Scales (R/S/G)
	wg.Add(1)
	go func() {
		defer wg.Done()
		r, s, g, err := parseNOAAScales(fetchJSON)
		if err != nil {
			log.Printf("weather: solar NOAA scales failed (non-fatal): %v", err)
			return
		}
		mu.Lock()
		sd.RadioScale = r
		sd.SolarScale = s
		sd.GeomagScale = g
		anySuccess = true
		mu.Unlock()
	}()

	// 4. X-ray flare class
	wg.Add(1)
	go func() {
		defer wg.Done()
		class, err := parseXRayFlare(fetchJSON)
		if err != nil {
			log.Printf("weather: solar X-ray flare failed (non-fatal): %v", err)
			return
		}
		mu.Lock()
		sd.XRayFlux = class
		anySuccess = true
		mu.Unlock()
	}()

	// 5. Kp index
	wg.Add(1)
	go func() {
		defer wg.Done()
		kp, err := parseKpIndex(fetchJSON)
		if err != nil {
			log.Printf("weather: solar Kp index failed (non-fatal): %v", err)
			return
		}
		mu.Lock()
		sd.KpIndex = kp
		anySuccess = true
		mu.Unlock()
	}()

	// 6. Solar wind speed
	wg.Add(1)
	go func() {
		defer wg.Done()
		speed, err := parseSolarWind(fetchJSON)
		if err != nil {
			log.Printf("weather: solar wind speed failed (non-fatal): %v", err)
			return
		}
		mu.Lock()
		sd.WindSpeedKms = speed
		anySuccess = true
		mu.Unlock()
	}()

	wg.Wait()

	if !anySuccess {
		return nil
	}
	return sd
}

// fetchBytesCtx fetches raw bytes from a URL using the given context.
func fetchBytesCtx(ctx context.Context, httpClient *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// fetchJSONCtx fetches and decodes JSON from a URL using the given context.
func fetchJSONCtx(ctx context.Context, httpClient *http.Client, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// parseNOAAScales extracts the current R, S, G scale values from the NOAA scales JSON.
// The JSON is an object with "0" (current) containing "R", "S", "G" objects
// each having a "Scale" field.
func parseNOAAScales(fetchJSON func(string, any) error) (r, s, g int, err error) {
	var raw map[string]json.RawMessage
	if err = fetchJSON(noaaScalesURL, &raw); err != nil {
		return
	}
	current, ok := raw["0"]
	if !ok {
		err = fmt.Errorf("no current scales in response")
		return
	}
	// Scale values come as JSON strings (e.g. "0"), not integers.
	var scales struct {
		R struct{ Scale *string } `json:"R"`
		S struct{ Scale *string } `json:"S"`
		G struct{ Scale *string } `json:"G"`
	}
	if err = json.Unmarshal(current, &scales); err != nil {
		return
	}
	if scales.R.Scale != nil {
		r, _ = strconv.Atoi(*scales.R.Scale)
	}
	if scales.S.Scale != nil {
		s, _ = strconv.Atoi(*scales.S.Scale)
	}
	if scales.G.Scale != nil {
		g, _ = strconv.Atoi(*scales.G.Scale)
	}
	return
}

// parseXRayFlare extracts the latest X-ray flare class from the NOAA flare JSON.
func parseXRayFlare(fetchJSON func(string, any) error) (string, error) {
	var flares []struct {
		CurrentClass string `json:"current_class"`
		MaxClass     string `json:"max_class"`
	}
	if err := fetchJSON(noaaXRayFlareURL, &flares); err != nil {
		return "", err
	}
	if len(flares) == 0 {
		return "", fmt.Errorf("no flare data in response")
	}
	// Use current_class if available, fall back to max_class.
	cls := flares[len(flares)-1].CurrentClass
	if cls == "" {
		cls = flares[len(flares)-1].MaxClass
	}
	return strings.TrimSpace(cls), nil
}

// parseKpIndex extracts the latest planetary Kp index from the NOAA JSON.
// Uses estimated_kp (fractional) rather than kp_index (rounded integer).
func parseKpIndex(fetchJSON func(string, any) error) (float64, error) {
	var entries []struct {
		EstimatedKp float64 `json:"estimated_kp"`
	}
	if err := fetchJSON(noaaKpIndexURL, &entries); err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, fmt.Errorf("no Kp data in response")
	}
	return entries[len(entries)-1].EstimatedKp, nil
}

// parseSolarWind extracts the latest solar wind speed from the NOAA plasma JSON.
// The JSON is a 2D array: first row is headers, subsequent rows are data.
// Speed is typically in column index 1 (after time_tag).
func parseSolarWind(fetchJSON func(string, any) error) (float64, error) {
	var rows [][]string
	if err := fetchJSON(noaaSolarWindURL, &rows); err != nil {
		return 0, err
	}
	if len(rows) < 2 {
		return 0, fmt.Errorf("insufficient solar wind data")
	}
	// Find the speed column from the header row.
	header := rows[0]
	speedCol := -1
	for i, h := range header {
		if strings.EqualFold(h, "speed") {
			speedCol = i
			break
		}
	}
	if speedCol < 0 {
		return 0, fmt.Errorf("speed column not found in solar wind data")
	}
	// Use the last row with valid data.
	for i := len(rows) - 1; i >= 1; i-- {
		row := rows[i]
		if speedCol >= len(row) || row[speedCol] == "" {
			continue
		}
		speed, err := strconv.ParseFloat(row[speedCol], 64)
		if err != nil {
			continue
		}
		return speed, nil
	}
	return 0, fmt.Errorf("no valid solar wind speed found")
}
