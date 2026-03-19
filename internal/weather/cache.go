package weather

import (
	"encoding/json"
	"os"
	"time"
)

// weatherCache is the on-disk representation of cached weather data.
// Binary fields (radar, satellite, static map, solar images) are excluded
// because they are large, time-sensitive, and cheap to re-fetch.
type weatherCache struct {
	FetchedAt      time.Time         `json:"fetchedAt"`
	Location       string            `json:"location"`
	Current        CurrentConditions `json:"current"`
	HourlyPeriods  []ForecastPeriod  `json:"hourlyPeriods,omitempty"`
	DailyPeriods   []ForecastPeriod  `json:"dailyPeriods,omitempty"`
	MoonPhase      MoonPhase         `json:"moonPhase"`
	Planets        *PlanetData       `json:"planets,omitempty"`
	TideData       *TideData         `json:"tideData,omitempty"`
	Alerts         []Alert           `json:"alerts,omitempty"`
	PrecipTotal24h float64           `json:"precipTotal24h"`
	SnowTotal24h   float64           `json:"snowTotal24h"`
	Sun            *SunData          `json:"sun,omitempty"`
	UVIndex        float64           `json:"uvIndex"`
	HourlyUV       []float64         `json:"hourlyUV,omitempty"`

	// Solar metrics (excluding images).
	SolarKpIndex      float64   `json:"solarKpIndex,omitempty"`
	SolarXRayFlux     string    `json:"solarXRayFlux,omitempty"`
	SolarRadioScale   int       `json:"solarRadioScale,omitempty"`
	SolarSolarScale   int       `json:"solarSolarScale,omitempty"`
	SolarGeomagScale  int       `json:"solarGeomagScale,omitempty"`
	SolarWindSpeedKms float64   `json:"solarWindSpeedKms,omitempty"`
	SolarFetchedAt    time.Time `json:"solarFetchedAt,omitempty"`
}

// saveCache writes the cacheable portions of WeatherData to disk as JSON.
func (c *Client) saveCache(data *WeatherData) {
	if c.cachePath == "" || data == nil {
		return
	}

	cache := weatherCache{
		FetchedAt:      data.FetchedAt,
		Location:       data.Location,
		Current:        data.Current,
		HourlyPeriods:  data.HourlyPeriods,
		DailyPeriods:   data.DailyPeriods,
		MoonPhase:      data.MoonPhase,
		Planets:        data.Planets,
		TideData:       data.TideData,
		Alerts:         data.Alerts,
		PrecipTotal24h: data.PrecipTotal24h,
		SnowTotal24h:   data.SnowTotal24h,
		Sun:            data.Sun,
		UVIndex:        data.UVIndex,
		HourlyUV:       data.HourlyUV,
	}

	if data.Solar != nil {
		cache.SolarKpIndex = data.Solar.KpIndex
		cache.SolarXRayFlux = data.Solar.XRayFlux
		cache.SolarRadioScale = data.Solar.RadioScale
		cache.SolarSolarScale = data.Solar.SolarScale
		cache.SolarGeomagScale = data.Solar.GeomagScale
		cache.SolarWindSpeedKms = data.Solar.WindSpeedKms
		cache.SolarFetchedAt = data.Solar.FetchedAt
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
	c.log.Printf("cache: saved to %s", c.cachePath)
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
		FetchedAt:      cache.FetchedAt,
		Location:       cache.Location,
		Current:        cache.Current,
		HourlyPeriods:  cache.HourlyPeriods,
		DailyPeriods:   cache.DailyPeriods,
		MoonPhase:      cache.MoonPhase,
		Planets:        cache.Planets,
		TideData:       cache.TideData,
		Alerts:         cache.Alerts,
		PrecipTotal24h: cache.PrecipTotal24h,
		SnowTotal24h:   cache.SnowTotal24h,
		Sun:            cache.Sun,
		UVIndex:        cache.UVIndex,
		HourlyUV:       cache.HourlyUV,
	}

	// Restore solar metrics (without images — those will come from the next solar refresh).
	if !cache.SolarFetchedAt.IsZero() {
		data.Solar = &SolarData{
			KpIndex:      cache.SolarKpIndex,
			XRayFlux:     cache.SolarXRayFlux,
			RadioScale:   cache.SolarRadioScale,
			SolarScale:   cache.SolarSolarScale,
			GeomagScale:  cache.SolarGeomagScale,
			WindSpeedKms: cache.SolarWindSpeedKms,
			FetchedAt:    cache.SolarFetchedAt,
		}
	}

	c.log.Printf("cache: loaded from %s (%.0fm old)", c.cachePath, age.Minutes())
	return data
}
