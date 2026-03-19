package weather

import (
	"encoding/json"
	"os"
	"time"
)

// weatherCache is the on-disk representation of cached weather data.
// All fields are included so a restart can fully restore the previous state
// without any API calls. Binary data (images) is base64-encoded by json.Marshal.
type weatherCache struct {
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

// saveCache writes WeatherData to disk as JSON.
func (c *Client) saveCache(data *WeatherData) {
	if c.cachePath == "" || data == nil {
		return
	}

	cache := weatherCache{
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
