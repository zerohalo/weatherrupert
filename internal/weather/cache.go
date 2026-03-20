package weather

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// weatherCache is the on-disk representation of cached weather data.
// Includes bootstrap state (grid, stations, tides) so a restart with fresh
// cache can skip all API calls. Binary data is base64-encoded by json.Marshal.
type weatherCache struct {
	// Bootstrap state.
	GridID        string       `json:"gridID"`
	GridX         int          `json:"gridX"`
	GridY         int          `json:"gridY"`
	StationIDs    []string     `json:"stationIDs,omitempty"`
	NearestTide   *TideStation `json:"nearestTide,omitempty"`
	TideDistMiles float64      `json:"tideDistMiles,omitempty"`

	// Weather data.
	FetchedAt       time.Time         `json:"fetchedAt"`
	Location        string            `json:"location"`
	Current         CurrentConditions `json:"current"`
	HourlyPeriods   []ForecastPeriod  `json:"hourlyPeriods,omitempty"`
	DailyPeriods    []ForecastPeriod  `json:"dailyPeriods,omitempty"`
	RadarFrames     [][]byte          `json:"radarFrames,omitempty"`
	SatelliteFrames [][]byte          `json:"satelliteFrames,omitempty"`
	StaticMap       []byte            `json:"staticMap,omitempty"`
	MoonPhase       MoonPhase         `json:"moonPhase"`
	Planets         *PlanetData       `json:"planets,omitempty"`
	TideData        *TideData         `json:"tideData,omitempty"`
	Alerts          []Alert           `json:"alerts,omitempty"`
	PrecipTotal24h  float64           `json:"precipTotal24h"`
	SnowTotal24h    float64           `json:"snowTotal24h"`
	Sun             *SunData          `json:"sun,omitempty"`
	UVIndex         float64           `json:"uvIndex"`
	HourlyUV        []float64         `json:"hourlyUV,omitempty"`
	Solar           *SolarData        `json:"solar,omitempty"`
}

// saveCache writes WeatherData and bootstrap state to disk as JSON.
func (c *Client) saveCache(data *WeatherData) {
	if c.cachePath == "" || data == nil {
		return
	}

	cache := weatherCache{
		// Bootstrap state.
		GridID:        c.gridID,
		GridX:         c.gridX,
		GridY:         c.gridY,
		StationIDs:    c.stationIDs,
		NearestTide:   c.nearestTide,
		TideDistMiles: c.tideDistMiles,

		// Weather data.
		FetchedAt:       data.FetchedAt,
		Location:        data.Location,
		Current:         data.Current,
		HourlyPeriods:   data.HourlyPeriods,
		DailyPeriods:    data.DailyPeriods,
		RadarFrames:     data.RadarFrames,
		SatelliteFrames: data.SatelliteFrames,
		StaticMap:       data.StaticMap,
		MoonPhase:       data.MoonPhase,
		Planets:         data.Planets,
		TideData:        data.TideData,
		Alerts:          data.Alerts,
		PrecipTotal24h:  data.PrecipTotal24h,
		SnowTotal24h:    data.SnowTotal24h,
		Sun:             data.Sun,
		UVIndex:         data.UVIndex,
		HourlyUV:        data.HourlyUV,
		Solar:           data.Solar,
	}

	b, err := json.Marshal(cache)
	if err != nil {
		c.log.Printf("cache: marshal failed: %v", err)
		return
	}

	if err := os.WriteFile(c.cachePath, b, 0644); err != nil {
		c.log.Printf("cache: write failed: %v", err)
		return
	}
	c.log.Printf("cache: saved to %s (%d bytes)", c.cachePath, len(b))
}

// readCache reads and parses the cache file. Returns nil if missing or unreadable.
func (c *Client) readCache() *weatherCache {
	if c.cachePath == "" {
		return nil
	}

	b, err := os.ReadFile(c.cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.log.Printf("cache: no cache file found at %s", c.cachePath)
		} else {
			c.log.Printf("cache: read failed: %v", err)
		}
		return nil
	}

	var cache weatherCache
	if err := json.Unmarshal(b, &cache); err != nil {
		c.log.Printf("cache: unmarshal failed: %v", err)
		return nil
	}

	c.log.Printf("cache: read %d bytes from %s", len(b), c.cachePath)
	return &cache
}

// RestoreFromCache attempts to load a cache and restore state per-component:
//
//   - Bootstrap state (grid, stations) — always restored if present (never stale).
//   - Weather data — restored if within weatherMaxAge; used as stale fallback
//     otherwise (better than nothing while fresh data is fetched).
//   - Solar data — restored if within solarRefreshInterval (1 hour).
//
// Returns true if bootstrap can be skipped (bootstrap state was restored).
// The caller should still check whether weather data needs a fresh fetch.
func (c *Client) RestoreFromCache(weatherMaxAge time.Duration) bool {
	c.log.Printf("cache: checking for cached data")
	cache := c.readCache()
	if cache == nil {
		return false
	}

	// Always restore bootstrap state — grid/station info doesn't go stale.
	bootstrapOK := false
	if cache.GridID != "" && len(cache.StationIDs) > 0 {
		c.gridID = cache.GridID
		c.gridX = cache.GridX
		c.gridY = cache.GridY
		c.stationIDs = cache.StationIDs
		c.nearestTide = cache.NearestTide
		c.tideDistMiles = cache.TideDistMiles
		c.staticMap = cache.StaticMap
		c.locationMu.Lock()
		c.location = cache.Location
		c.locationMu.Unlock()
		bootstrapOK = true
		c.log.Printf("cache: restored bootstrap state (grid %s/%d,%d, %d stations)",
			c.gridID, c.gridX, c.gridY, len(c.stationIDs))
	}

	// Restore weather data. Even if stale, it's better than a blank screen.
	weatherAge := time.Since(cache.FetchedAt)
	weatherFresh := weatherAge <= weatherMaxAge

	data := &WeatherData{
		FetchedAt:       cache.FetchedAt,
		Location:        cache.Location,
		Current:         cache.Current,
		HourlyPeriods:   cache.HourlyPeriods,
		DailyPeriods:    cache.DailyPeriods,
		RadarFrames:     cache.RadarFrames,
		SatelliteFrames: cache.SatelliteFrames,
		StaticMap:       cache.StaticMap,
		MoonPhase:       cache.MoonPhase,
		Planets:         cache.Planets,
		TideData:        cache.TideData,
		Alerts:          cache.Alerts,
		PrecipTotal24h:  cache.PrecipTotal24h,
		SnowTotal24h:    cache.SnowTotal24h,
		Sun:             cache.Sun,
		UVIndex:         cache.UVIndex,
		HourlyUV:        cache.HourlyUV,
	}

	// Include cached solar data in the weather snapshot. The shared solar
	// pointer is managed by the Manager; we just include it here so slides
	// have something to show until the shared refresh runs.
	if cache.Solar != nil {
		data.Solar = cache.Solar
		c.log.Printf("cache: solar data included (%.0fm old)", time.Since(cache.Solar.FetchedAt).Minutes())
	}

	c.data.Store(data)

	if weatherFresh {
		tempLog := "n/a"
		if data.Current.TempF != nil {
			tempLog = fmt.Sprintf("%.1f°F", *data.Current.TempF)
		}
		c.log.Printf("cache: weather data fresh for %s (%s, %s) — %.0fm old",
			data.Location, tempLog, data.Current.Description, weatherAge.Minutes())
	} else {
		c.log.Printf("cache: weather data stale (%.0fm old, max %.0fm) — using as fallback, will refresh",
			weatherAge.Minutes(), weatherMaxAge.Minutes())
	}

	c.weatherFresh = weatherFresh
	return bootstrapOK
}
