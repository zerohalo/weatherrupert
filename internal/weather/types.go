package weather

import (
	"fmt"
	"math"
	"time"
)

// PointsResponse is the NWS /api/points/{lat},{lon} response.
type PointsResponse struct {
	Properties struct {
		GridID  string `json:"gridId"`
		GridX   int    `json:"gridX"`
		GridY   int    `json:"gridY"`
		Forecast        string `json:"forecast"`
		ForecastHourly  string `json:"forecastHourly"`
		ObservationStations string `json:"observationStations"`
		RelativeLocation struct {
			Properties struct {
				City  string `json:"city"`
				State string `json:"state"`
			} `json:"properties"`
		} `json:"relativeLocation"`
	} `json:"properties"`
}

// StationsResponse is the NWS stations feature collection response.
type StationsResponse struct {
	Features []struct {
		Properties struct {
			StationIdentifier string `json:"stationIdentifier"`
			Name              string `json:"name"`
		} `json:"properties"`
	} `json:"features"`
}

// ForecastResponse is the NWS forecast/hourly response.
type ForecastResponse struct {
	Properties struct {
		Periods []ForecastPeriod `json:"periods"`
	} `json:"properties"`
}

// ForecastPeriod represents a single forecast period.
type ForecastPeriod struct {
	Number           int    `json:"number"`
	Name             string `json:"name"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	IsDaytime        bool   `json:"isDaytime"`
	Temperature      int    `json:"temperature"`
	TemperatureUnit  string `json:"temperatureUnit"`
	WindSpeed        string `json:"windSpeed"`
	WindDirection    string `json:"windDirection"`
	ShortForecast    string `json:"shortForecast"`
	DetailedForecast string `json:"detailedForecast"`
	ProbabilityOfPrecipitation struct {
		Value *int `json:"value"`
	} `json:"probabilityOfPrecipitation"`
}

// Alert represents a single NWS weather alert.
type Alert struct {
	Event       string    // e.g. "Winter Storm Warning"
	Severity    string    // "Extreme", "Severe", "Moderate", "Minor"
	Headline    string
	Description string
	Onset       time.Time
	Expires     time.Time
}

// nullableFloat is a float64 that may be null in JSON.
type nullableFloat struct {
	Value *float64
}

func (n *nullableFloat) UnmarshalJSON(data []byte) error {
	s := string(data)
	if s == "null" {
		n.Value = nil
		return nil
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// ObservationResponse is the NWS /stations/{id}/observations/latest response.
type ObservationResponse struct {
	Properties struct {
		Timestamp       string `json:"timestamp"`
		TextDescription string `json:"textDescription"`
		Temperature     struct {
			Value nullableFloat `json:"value"`
		} `json:"temperature"`
		Dewpoint struct {
			Value nullableFloat `json:"value"`
		} `json:"dewpoint"`
		WindDirection struct {
			Value nullableFloat `json:"value"`
		} `json:"windDirection"`
		WindSpeed struct {
			Value nullableFloat `json:"value"`
		} `json:"windSpeed"`
		WindGust struct {
			Value nullableFloat `json:"value"`
		} `json:"windGust"`
		BarometricPressure struct {
			Value nullableFloat `json:"value"`
		} `json:"barometricPressure"`
		SeaLevelPressure struct {
			Value nullableFloat `json:"value"`
		} `json:"seaLevelPressure"`
		Visibility struct {
			Value nullableFloat `json:"value"`
		} `json:"visibility"`
		RelativeHumidity struct {
			Value nullableFloat `json:"value"`
		} `json:"relativeHumidity"`
		HeatIndex struct {
			Value nullableFloat `json:"value"`
		} `json:"heatIndex"`
		WindChill struct {
			Value nullableFloat `json:"value"`
		} `json:"windChill"`
	} `json:"properties"`
}

// CurrentConditions holds processed current weather conditions in Imperial units.
// Pointer fields are nil when the NWS observation value was null.
type CurrentConditions struct {
	Description  string
	TempF        *float64
	DewpointF    *float64
	Humidity     *float64
	WindDir      string
	WindSpeedMph *float64
	WindGustMph  *float64
	PressureInHg *float64
	VisibilityMi *float64
	HeatIndexF   *float64
	WindChillF   *float64
	UpdatedAt    time.Time
}

// gridPointRaw is the NWS raw gridpoint response (/gridpoints/{gridID}/{gridX},{gridY}).
type gridPointRaw struct {
	Properties struct {
		QuantitativePrecipitation gridTimeSeries `json:"quantitativePrecipitation"`
		SnowfallAmount            gridTimeSeries `json:"snowfallAmount"`
	} `json:"properties"`
}

// gridTimeSeries is a time-series layer from the raw gridpoint response.
type gridTimeSeries struct {
	Uom    string          `json:"uom"`
	Values []gridTimeValue `json:"values"`
}

// gridTimeValue is a single value in a gridTimeSeries.
type gridTimeValue struct {
	ValidTime string   `json:"validTime"`
	Value     *float64 `json:"value"`
}

// PlanetInfo holds computed position and visibility data for a single planet.
type PlanetInfo struct {
	Name        string     // "Mercury", "Venus", "Mars", "Jupiter", "Saturn"
	Altitude    float64    // degrees above horizon (negative = below)
	Azimuth     float64    // degrees: 0=N, 90=E, 180=S, 270=W
	Magnitude   float64    // visual magnitude (lower = brighter)
	RiseTime    *time.Time // nil if doesn't rise today
	SetTime     *time.Time // nil if doesn't set today
	TransitTime *time.Time // nil if doesn't transit today
	Compass     string     // cardinal direction: "N", "NE", "E", etc.
	IsUp        bool       // currently above the horizon
}

// PlanetData holds computed positions for the five naked-eye planets.
// Both live and sunset positions are precomputed so the renderer can
// switch between them instantly when the admin setting changes.
type PlanetData struct {
	LivePlanets   []PlanetInfo // positions at ComputedAt (current time)
	SunsetPlanets []PlanetInfo // positions at sunset; nil when after sunset or polar
	ComputedAt    time.Time    // wall clock when computation was requested
	SunsetTime    *time.Time   // today's sunset; nil if sun doesn't set (polar)
	BeforeSunset  bool         // true when ComputedAt is before SunsetTime
}

// SolarData holds solar activity metrics and images from NOAA SWPC and NASA SDO.
type SolarData struct {
	SunspotImage []byte    // SDO HMIIC JPEG (visible light)
	CoronaImage  []byte    // SDO AIA 304 JPEG (chromosphere)
	KpIndex      float64   // planetary Kp (0-9)
	XRayFlux     string    // current X-ray class e.g. "C2.1", "M1.5", "B3.4"
	RadioScale   int       // NOAA R-scale 0-5 (radio blackout)
	SolarScale   int       // NOAA S-scale 0-5 (solar radiation storm)
	GeomagScale  int       // NOAA G-scale 0-5 (geomagnetic storm)
	WindSpeedKms float64   // solar wind speed km/s
	FetchedAt    time.Time
}

// WeatherData is the consolidated view of all fetched weather data.
// It is treated as immutable once stored in the atomic pointer.
type WeatherData struct {
	Location      string
	FetchedAt     time.Time
	Current       CurrentConditions
	HourlyPeriods []ForecastPeriod
	DailyPeriods  []ForecastPeriod
	RadarFrames     [][]byte   // radar animation frames, oldest→newest; nil/empty when unavailable
	SatelliteFrames [][]byte   // GOES visible satellite frames, oldest→newest; nil/empty when unavailable
	StaticMap       []byte     // fallback basemap PNG when radar is unavailable
	MoonPhase     MoonPhase  // always populated (computed algorithmically)
	Planets       *PlanetData // always populated (computed algorithmically)
	TideData      *TideData  // nil when inland / no nearby station
	Alerts        []Alert    // active NWS alerts; nil/empty when none
	Solar          *SolarData // nil when fetch failed entirely
	PrecipTotal24h float64   // expected liquid precip next 24h, in mm
	SnowTotal24h   float64   // expected snowfall next 24h, in mm
}

// Unit conversion helpers.

func celsiusToFahrenheit(c float64) float64 {
	return c*9/5 + 32
}

func kmhToMph(kmh float64) float64 {
	return kmh * 0.621371
}

func paToInHg(pa float64) float64 {
	return pa * 0.0002952998
}

func mToMiles(m float64) float64 {
	return m * 0.000621371
}

func degreesToCardinal(deg float64) string {
	dirs := []string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE",
		"S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}
	ix := int(math.Round(deg/22.5)) % 16
	if ix < 0 {
		ix += 16
	}
	return dirs[ix]
}

// f64ptr is a helper that returns a pointer to a float64 value.
func f64ptr(v float64) *float64 { return &v }

// parseObservation converts raw SI observation values to Imperial CurrentConditions.
func parseObservation(obs *ObservationResponse) CurrentConditions {
	c := CurrentConditions{
		Description: obs.Properties.TextDescription,
	}

	if v := obs.Properties.Temperature.Value.Value; v != nil {
		c.TempF = f64ptr(celsiusToFahrenheit(*v))
	}
	if v := obs.Properties.Dewpoint.Value.Value; v != nil {
		c.DewpointF = f64ptr(celsiusToFahrenheit(*v))
	}
	if v := obs.Properties.RelativeHumidity.Value.Value; v != nil {
		c.Humidity = f64ptr(*v)
	}
	if v := obs.Properties.WindDirection.Value.Value; v != nil {
		c.WindDir = degreesToCardinal(*v)
	}
	if v := obs.Properties.WindSpeed.Value.Value; v != nil {
		c.WindSpeedMph = f64ptr(kmhToMph(*v))
	}
	if v := obs.Properties.WindGust.Value.Value; v != nil {
		c.WindGustMph = f64ptr(kmhToMph(*v))
	}

	// Use sea level pressure if available, fall back to barometric
	if v := obs.Properties.SeaLevelPressure.Value.Value; v != nil {
		c.PressureInHg = f64ptr(paToInHg(*v))
	} else if v := obs.Properties.BarometricPressure.Value.Value; v != nil {
		c.PressureInHg = f64ptr(paToInHg(*v))
	}

	if v := obs.Properties.Visibility.Value.Value; v != nil {
		c.VisibilityMi = f64ptr(mToMiles(*v))
	}
	if v := obs.Properties.HeatIndex.Value.Value; v != nil {
		c.HeatIndexF = f64ptr(celsiusToFahrenheit(*v))
	}
	if v := obs.Properties.WindChill.Value.Value; v != nil {
		c.WindChillF = f64ptr(celsiusToFahrenheit(*v))
	}

	if ts := obs.Properties.Timestamp; ts != "" {
		t, err := time.Parse(time.RFC3339, ts)
		if err == nil {
			c.UpdatedAt = t
		}
	}

	return c
}
