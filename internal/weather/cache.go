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

// loadCache reads cached weather data from disk. Returns nil if the cache
// is missing, unreadable, or older than maxAge.
func (c *Client) loadCache(maxAge time.Duration) *WeatherData {
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

	age := time.Since(cache.FetchedAt)
	if age > maxAge {
		c.log.Printf("cache: stale (%.0fm old, max %.0fm), will fetch fresh data", age.Minutes(), maxAge.Minutes())
		return nil
	}

	// Restore bootstrap state so fetch() can work without re-bootstrapping.
	if cache.GridID != "" {
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
	}

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
		Solar:           cache.Solar,
	}

	c.log.Printf("cache: loaded from %s (%d bytes, %.0fm old)", c.cachePath, len(b), age.Minutes())
	return data
}

// RestoreFromCache attempts to load a fresh cache and fully restore both
// bootstrap state and weather data. Returns true if successful, meaning
// Bootstrap() and the initial fetch can be skipped entirely.
func (c *Client) RestoreFromCache(maxAge time.Duration) bool {
	data := c.loadCache(maxAge)
	if data == nil {
		return false
	}
	// Verify bootstrap state was restored (gridID is required for fetch).
	if c.gridID == "" || len(c.stationIDs) == 0 {
		c.log.Printf("cache: missing bootstrap state, will bootstrap normally")
		return false
	}
	c.data.Store(data)
	if data.Solar != nil {
		c.solarData.Store(data.Solar)
	}
	tempLog := "n/a"
	if data.Current.TempF != nil {
		tempLog = fmt.Sprintf("%.1f°F", *data.Current.TempF)
	}
	c.log.Printf("restored from cache for %s (%s, %s) — %.0fm old, skipping bootstrap",
		data.Location, tempLog, data.Current.Description,
		time.Since(data.FetchedAt).Minutes())
	return true
}
