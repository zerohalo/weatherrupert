package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// alertsResponse is the GeoJSON FeatureCollection returned by /alerts/active.
type alertsResponse struct {
	Features []alertFeature `json:"features"`
}

type alertFeature struct {
	Properties alertProperties `json:"properties"`
}

type alertProperties struct {
	Event       string `json:"event"`
	Severity    string `json:"severity"`
	Headline    string `json:"headline"`
	Description string `json:"description"`
	Onset       string `json:"onset"`
	Expires     string `json:"expires"`
	Status      string `json:"status"`
}

// fetchAlerts fetches active NWS alerts for the given lat/lon.
// Deduplicates by event name, keeping the first (most recent) instance.
func fetchAlerts(ctx context.Context, baseURL string, lat, lon float64, client *http.Client) []Alert {
	url := fmt.Sprintf("%s/alerts/active?point=%.4f,%.4f&status=actual", baseURL, lat, lon)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Printf("weather: alerts request: %v", err)
		return nil
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/geo+json,application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("weather: alerts fetch: %v", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("weather: alerts HTTP %d", resp.StatusCode)
		return nil
	}

	var ar alertsResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		log.Printf("weather: alerts decode: %v", err)
		return nil
	}

	seen := make(map[string]bool)
	var alerts []Alert
	for _, f := range ar.Features {
		p := f.Properties
		if p.Status != "" && p.Status != "actual" {
			continue
		}
		if seen[p.Event] {
			continue
		}
		seen[p.Event] = true

		a := Alert{
			Event:       p.Event,
			Severity:    p.Severity,
			Headline:    p.Headline,
			Description: p.Description,
		}
		if t, err := time.Parse(time.RFC3339, p.Onset); err == nil {
			a.Onset = t
		}
		if t, err := time.Parse(time.RFC3339, p.Expires); err == nil {
			a.Expires = t
		}
		alerts = append(alerts, a)
	}
	return alerts
}
