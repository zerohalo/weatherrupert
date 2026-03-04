package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/zerohalo/weatherrupert/internal/apiurl"
)

// TideStation represents a NOAA tide prediction station.
type TideStation struct {
	ID   string
	Name string
	Lat  float64
	Lon  float64
}

// TidePrediction is a single hourly tide level reading.
type TidePrediction struct {
	Time  time.Time
	Level float64 // feet, MLLW datum
}

// TideHiLo represents a single high or low tide event with its exact time.
type TideHiLo struct {
	Type  string    // "H" (high) or "L" (low)
	Time  time.Time
	Level float64
}

// TideData holds the nearest station info and 24h hourly predictions.
type TideData struct {
	Station       TideStation
	DistanceMiles float64
	Predictions   []TidePrediction
	HiLo          []TideHiLo // exact high/low tide events from NOAA
}

// noaaStationsResp is the JSON shape returned by the NOAA stations endpoint.
type noaaStationsResp struct {
	Stations []struct {
		ID   string  `json:"id"`
		Name string  `json:"name"`
		Lat  float64 `json:"lat"`
		Lng  float64 `json:"lng"`
	} `json:"stations"`
}

// noaaPredictionsResp is the JSON shape returned by the NOAA predictions endpoint.
type noaaPredictionsResp struct {
	Predictions []struct {
		T string `json:"t"` // "2024-01-15 03:00"
		V string `json:"v"` // "2.345"
	} `json:"predictions"`
}

// fetchTideStations fetches all NOAA tide prediction stations.
func fetchTideStations(ctx context.Context, client *http.Client) ([]TideStation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiurl.TideStations, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NOAA stations: HTTP %d", resp.StatusCode)
	}

	var raw noaaStationsResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("NOAA stations decode: %w", err)
	}

	stations := make([]TideStation, 0, len(raw.Stations))
	for _, s := range raw.Stations {
		stations = append(stations, TideStation{
			ID:   s.ID,
			Name: s.Name,
			Lat:  s.Lat,
			Lon:  s.Lng,
		})
	}
	return stations, nil
}

// findNearestStation returns the closest tide station within maxMiles, or nil.
func findNearestStation(stations []TideStation, lat, lon, maxMiles float64) (*TideStation, float64) {
	var best *TideStation
	bestDist := math.MaxFloat64

	for i := range stations {
		d := haversineDistanceMiles(lat, lon, stations[i].Lat, stations[i].Lon)
		if d < bestDist {
			bestDist = d
			best = &stations[i]
		}
	}
	if best == nil || bestDist > maxMiles {
		return nil, 0
	}
	return best, bestDist
}

// fetchTidePredictions fetches 24h hourly tide predictions for a station.
func fetchTidePredictions(ctx context.Context, client *http.Client, stationID string, date time.Time) ([]TidePrediction, error) {
	dateStr := date.Format("20060102")
	url := fmt.Sprintf(
		"%s?product=predictions&datum=MLLW&station=%s&time_zone=lst_ldt&units=english&interval=h&format=json&begin_date=%s&range=24&application=weatherrupert",
		apiurl.TidePredictions, stationID, dateStr,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NOAA predictions: HTTP %d", resp.StatusCode)
	}

	var raw noaaPredictionsResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("NOAA predictions decode: %w", err)
	}

	preds := make([]TidePrediction, 0, len(raw.Predictions))
	for _, p := range raw.Predictions {
		t, err := time.ParseInLocation("2006-01-02 15:04", p.T, date.Location())
		if err != nil {
			continue
		}
		var level float64
		if _, err := fmt.Sscanf(p.V, "%f", &level); err != nil {
			continue
		}
		preds = append(preds, TidePrediction{Time: t, Level: level})
	}
	return preds, nil
}

// noaaHiLoResp is the JSON shape returned by the NOAA hilo predictions endpoint.
type noaaHiLoResp struct {
	Predictions []struct {
		T    string `json:"t"`    // "2024-01-15 03:24"
		V    string `json:"v"`    // "4.567"
		Type string `json:"type"` // "H" or "L"
	} `json:"predictions"`
}

// fetchTideHiLo fetches the exact high/low tide events for a 24h period.
func fetchTideHiLo(ctx context.Context, client *http.Client, stationID string, date time.Time) ([]TideHiLo, error) {
	dateStr := date.Format("20060102")
	url := fmt.Sprintf(
		"%s?product=predictions&datum=MLLW&station=%s&time_zone=lst_ldt&units=english&interval=hilo&format=json&begin_date=%s&range=24&application=weatherrupert",
		apiurl.TideHiLo, stationID, dateStr,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NOAA hilo: HTTP %d", resp.StatusCode)
	}

	var raw noaaHiLoResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("NOAA hilo decode: %w", err)
	}

	results := make([]TideHiLo, 0, len(raw.Predictions))
	for _, p := range raw.Predictions {
		t, err := time.ParseInLocation("2006-01-02 15:04", p.T, date.Location())
		if err != nil {
			continue
		}
		var level float64
		if _, err := fmt.Sscanf(p.V, "%f", &level); err != nil {
			continue
		}
		results = append(results, TideHiLo{Type: p.Type, Time: t, Level: level})
	}
	return results, nil
}

// haversineDistanceMiles computes great-circle distance in miles.
func haversineDistanceMiles(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusMi = 3958.8
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMi * c
}
