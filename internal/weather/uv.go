package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/zerohalo/weatherrupert/internal/apiurl"
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
	// UVI ≈ 10 * sin(elevation)^2 * seasonal/latitude corrections
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

	uvi := 10.0 * math.Pow(sinElev, 2.0) * seasonFactor * latFactor

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

// epaUVHourly is a single entry from the EPA UV hourly forecast API.
type epaUVHourly struct {
	Order    int    `json:"ORDER"`
	DateTime string `json:"DATE_TIME"` // "Mar/24/2026 06 AM"
	UVValue  int    `json:"UV_VALUE"`
}

// fetchEPAUV fetches the hourly UV index forecast from the EPA Envirofacts API.
// Returns (current UV, hourly UV slice aligned to the provided hourly periods, ok).
// On any error it returns ok=false so the caller can fall back to the computed model.
func fetchEPAUV(ctx context.Context, client *http.Client, zip string, loc *time.Location, hourlyPeriods []ForecastPeriod) (float64, []float64, bool) {
	if zip == "" {
		return 0, nil, false
	}

	url := fmt.Sprintf("%s/ZIP/%s/JSON", apiurl.EPAUV, zip)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, false
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, nil, false
	}

	var entries []epaUVHourly
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return 0, nil, false
	}
	if len(entries) == 0 {
		return 0, nil, false
	}

	// Parse EPA entries into a map of local hour → UV value.
	// EPA DATE_TIME format: "Mar/24/2026 06 AM"
	hourUV := make(map[int]int, len(entries))
	for _, e := range entries {
		t, err := time.Parse("Jan/02/2006 03 PM", e.DateTime)
		if err != nil {
			continue
		}
		hourUV[t.Hour()] = e.UVValue
	}

	// Find current UV: use the entry matching the current hour.
	now := time.Now().In(loc)
	currentUV := float64(hourUV[now.Hour()])

	// Align EPA data to the hourly forecast periods.
	hourlyUV := make([]float64, len(hourlyPeriods))
	matched := 0
	for i, p := range hourlyPeriods {
		t, err := time.Parse(time.RFC3339, p.StartTime)
		if err != nil {
			continue
		}
		t = t.In(loc)
		if uv, ok := hourUV[t.Hour()]; ok {
			hourlyUV[i] = float64(uv)
			matched++
		}
	}

	// If we matched fewer than 3 hours, the data is too sparse to be useful.
	if matched < 3 {
		return 0, nil, false
	}

	return currentUV, hourlyUV, true
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
