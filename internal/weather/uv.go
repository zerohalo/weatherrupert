package weather

import (
	"math"
	"strings"
	"time"
)

// ComputeUVIndex estimates the UV index from solar elevation angle, day of
// year, and cloud cover. This is a simplified clear-sky model with a cloud
// attenuation factor — accurate enough for display purposes without requiring
// an external API.
func ComputeUVIndex(t time.Time, lat, lon float64, cloudDesc string) float64 {
	latRad := deg2rad(lat)
	alt := sunAltitude(t, lat, lon, latRad)
	if alt <= 0 {
		return 0 // sun below horizon
	}

	// Clear-sky UV index approximation based on solar zenith angle.
	// UVI ≈ 12.5 * sin(elevation)^1.3 * ozone_factor
	sinElev := math.Sin(deg2rad(alt))

	// Ozone thickness varies with latitude and season — approximate with
	// a factor that peaks in spring/summer at mid-latitudes.
	doy := float64(t.YearDay())
	// Northern hemisphere summer peak near day 172 (June 21); shifted for southern.
	if lat < 0 {
		doy = math.Mod(doy+182, 365)
	}
	seasonFactor := 1.0 + 0.15*math.Cos(2*math.Pi*(doy-172)/365)

	// Latitude factor: UV is higher at equator, lower at poles.
	latFactor := 1.0 + 0.15*(1-math.Abs(lat)/90)

	uvi := 12.0 * math.Pow(sinElev, 1.3) * seasonFactor * latFactor

	// Cloud attenuation factor based on forecast description.
	cloudFactor := cloudAttenuationFactor(cloudDesc)
	uvi *= cloudFactor

	if uvi < 0 {
		uvi = 0
	}
	return math.Round(uvi*10) / 10
}

// cloudAttenuationFactor returns a UV transmittance multiplier based on
// the NWS short forecast text.
func cloudAttenuationFactor(desc string) float64 {
	s := strings.ToLower(desc)
	switch {
	case strings.Contains(s, "overcast") || strings.Contains(s, "heavy rain") || strings.Contains(s, "thunderstorm"):
		return 0.3
	case strings.Contains(s, "cloudy") && !strings.Contains(s, "partly") && !strings.Contains(s, "mostly sunny"):
		return 0.5
	case strings.Contains(s, "mostly cloudy"):
		return 0.6
	case strings.Contains(s, "partly cloudy") || strings.Contains(s, "partly sunny"):
		return 0.8
	case strings.Contains(s, "mostly sunny") || strings.Contains(s, "mostly clear"):
		return 0.9
	default:
		return 1.0 // clear/sunny
	}
}

// UVCategory returns the EPA UV index category name.
func UVCategory(uvi float64) string {
	switch {
	case uvi < 3:
		return "Low"
	case uvi < 6:
		return "Moderate"
	case uvi < 8:
		return "High"
	case uvi < 11:
		return "Very High"
	default:
		return "Extreme"
	}
}
