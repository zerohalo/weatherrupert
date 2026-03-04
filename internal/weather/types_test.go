package weather

import (
	"math"
	"testing"
	"time"
)

func TestCelsiusToFahrenheit(t *testing.T) {
	tests := []struct {
		c, want float64
	}{
		{0, 32},
		{100, 212},
		{-40, -40},
		{37, 98.6},
	}
	for _, tt := range tests {
		got := celsiusToFahrenheit(tt.c)
		if math.Abs(got-tt.want) > 0.01 {
			t.Errorf("celsiusToFahrenheit(%v) = %v, want %v", tt.c, got, tt.want)
		}
	}
}

func TestKmhToMph(t *testing.T) {
	tests := []struct {
		kmh, want float64
	}{
		{0, 0},
		{100, 62.1371},
		{1.60934, 1.0},
	}
	for _, tt := range tests {
		got := kmhToMph(tt.kmh)
		if math.Abs(got-tt.want) > 0.01 {
			t.Errorf("kmhToMph(%v) = %v, want %v", tt.kmh, got, tt.want)
		}
	}
}

func TestPaToInHg(t *testing.T) {
	// 101325 Pa = 1 atm ≈ 29.92 inHg
	got := paToInHg(101325)
	if math.Abs(got-29.92) > 0.01 {
		t.Errorf("paToInHg(101325) = %v, want ~29.92", got)
	}
}

func TestMToMiles(t *testing.T) {
	// 1609.34 meters ≈ 1 mile
	got := mToMiles(1609.34)
	if math.Abs(got-1.0) > 0.001 {
		t.Errorf("mToMiles(1609.34) = %v, want ~1.0", got)
	}
}

func TestDegreesToCardinal(t *testing.T) {
	tests := []struct {
		deg  float64
		want string
	}{
		{0, "N"},
		{45, "NE"},
		{90, "E"},
		{135, "SE"},
		{180, "S"},
		{225, "SW"},
		{270, "W"},
		{315, "NW"},
		{360, "N"},
		{350, "N"},  // rounds to N
		{11, "N"},   // still N
		{12, "NNE"}, // just past boundary
	}
	for _, tt := range tests {
		got := degreesToCardinal(tt.deg)
		if got != tt.want {
			t.Errorf("degreesToCardinal(%v) = %q, want %q", tt.deg, got, tt.want)
		}
	}
}

func TestParseObservation(t *testing.T) {
	v := func(f float64) nullableFloat { return nullableFloat{Value: &f} }

	obs := &ObservationResponse{}
	obs.Properties.TextDescription = "Partly Cloudy"
	obs.Properties.Timestamp = "2026-03-01T14:00:00+00:00"
	obs.Properties.Temperature.Value = v(22.22)                // 72°F
	obs.Properties.Dewpoint.Value = v(11.11)                   // ~52°F
	obs.Properties.RelativeHumidity.Value = v(45)              // pass-through
	obs.Properties.WindDirection.Value = v(315)                 // NW
	obs.Properties.WindSpeed.Value = v(12.875)                 // ~8 mph
	obs.Properties.BarometricPressure.Value = v(101990)        // ~30.12 inHg
	obs.Properties.Visibility.Value = v(16093.4)               // ~10 mi

	c := parseObservation(obs)

	if c.Description != "Partly Cloudy" {
		t.Errorf("Description = %q, want %q", c.Description, "Partly Cloudy")
	}
	if c.TempF == nil || math.Abs(*c.TempF-72.0) > 0.1 {
		t.Errorf("TempF = %v, want ~72.0", c.TempF)
	}
	if c.WindDir != "NW" {
		t.Errorf("WindDir = %q, want %q", c.WindDir, "NW")
	}
	if c.Humidity == nil || *c.Humidity != 45 {
		t.Errorf("Humidity = %v, want 45", c.Humidity)
	}
	if c.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be parsed")
	}
}

func TestParseObservationNulls(t *testing.T) {
	obs := &ObservationResponse{}
	obs.Properties.TextDescription = "Fair"

	c := parseObservation(obs)
	if c.TempF != nil {
		t.Errorf("TempF should be nil, got %v", *c.TempF)
	}
	if c.WindDir != "" {
		t.Errorf("WindDir should be empty, got %q", c.WindDir)
	}
	if c.PressureInHg != nil {
		t.Errorf("PressureInHg should be nil, got %v", *c.PressureInHg)
	}
}

func TestParseObservationSeaLevelPressureFallback(t *testing.T) {
	v := func(f float64) nullableFloat { return nullableFloat{Value: &f} }

	// When both sea level and barometric are set, sea level wins.
	obs := &ObservationResponse{}
	obs.Properties.SeaLevelPressure.Value = v(101325) // ~29.92
	obs.Properties.BarometricPressure.Value = v(99000)

	c := parseObservation(obs)
	if c.PressureInHg == nil || math.Abs(*c.PressureInHg-29.92) > 0.01 {
		t.Errorf("PressureInHg = %v, want ~29.92 (sea level)", c.PressureInHg)
	}

	// When sea level is nil, barometric is used.
	obs2 := &ObservationResponse{}
	obs2.Properties.BarometricPressure.Value = v(101325)

	c2 := parseObservation(obs2)
	if c2.PressureInHg == nil || math.Abs(*c2.PressureInHg-29.92) > 0.01 {
		t.Errorf("PressureInHg fallback = %v, want ~29.92", c2.PressureInHg)
	}
}

func TestParseISO8601Duration(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"PT1H", time.Hour, false},
		{"PT30M", 30 * time.Minute, false},
		{"PT2H30M", 2*time.Hour + 30*time.Minute, false},
		{"P1D", 24 * time.Hour, false},
		{"P2DT3H", 2*24*time.Hour + 3*time.Hour, false},
		{"P1DT12H30M", 24*time.Hour + 12*time.Hour + 30*time.Minute, false},
		{"PT0H", 0, false},
		{"P0D", 0, false},
		// Errors
		{"", 0, true},           // no P prefix
		{"T1H", 0, true},       // missing P
		{"PTabcH", 0, true},    // bad hours
		{"PTxyzM", 0, true},    // bad minutes
		{"PabcD", 0, true},     // bad days
	}
	for _, tt := range tests {
		got, err := parseISO8601Duration(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseISO8601Duration(%q) = %v, want error", tt.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseISO8601Duration(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseISO8601Duration(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseValidTime(t *testing.T) {
	start, dur, err := parseValidTime("2026-02-28T16:00:00+00:00/PT1H")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantStart := time.Date(2026, 2, 28, 16, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Errorf("start = %v, want %v", start, wantStart)
	}
	if dur != time.Hour {
		t.Errorf("duration = %v, want 1h", dur)
	}

	// Missing slash.
	_, _, err = parseValidTime("2026-02-28T16:00:00+00:00")
	if err == nil {
		t.Error("expected error for missing slash")
	}

	// Bad timestamp.
	_, _, err = parseValidTime("not-a-time/PT1H")
	if err == nil {
		t.Error("expected error for bad timestamp")
	}

	// Bad duration.
	_, _, err = parseValidTime("2026-02-28T16:00:00+00:00/NOTADURATION")
	if err == nil {
		t.Error("expected error for bad duration")
	}
}

func TestSumGridSeries(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	series := gridTimeSeries{
		Values: []gridTimeValue{
			{ValidTime: "2026-03-01T00:00:00+00:00/PT1H", Value: f(1.0)},
			{ValidTime: "2026-03-01T01:00:00+00:00/PT1H", Value: f(2.0)},
			{ValidTime: "2026-03-01T02:00:00+00:00/PT1H", Value: f(3.0)},
			{ValidTime: "2026-03-01T03:00:00+00:00/PT1H", Value: nil},    // null, skip
			{ValidTime: "2026-03-02T00:00:00+00:00/PT1H", Value: f(10.0)}, // outside range
		},
	}

	// Sum first 3 hours: 1 + 2 + 3 = 6
	got := sumGridSeries(series, base, base.Add(3*time.Hour))
	if got != 6.0 {
		t.Errorf("sumGridSeries = %v, want 6.0", got)
	}

	// Sum all 24h: 1 + 2 + 3 = 6 (null skipped, last one outside)
	got = sumGridSeries(series, base, base.Add(4*time.Hour))
	if got != 6.0 {
		t.Errorf("sumGridSeries 4h = %v, want 6.0", got)
	}

	// Partial overlap: query [01:30, 03:00) overlaps hours 1 and 2
	got = sumGridSeries(series, base.Add(90*time.Minute), base.Add(3*time.Hour))
	if got != 5.0 {
		t.Errorf("sumGridSeries partial = %v, want 5.0 (2+3)", got)
	}

	// Empty series.
	got = sumGridSeries(gridTimeSeries{}, base, base.Add(time.Hour))
	if got != 0 {
		t.Errorf("sumGridSeries empty = %v, want 0", got)
	}
}

func TestRewriteToBase(t *testing.T) {
	tests := []struct {
		base, url, want string
	}{
		{
			"http://proxy:8080/api",
			"https://api.weather.gov/gridpoints/OKX/33,37",
			"http://proxy:8080/api/gridpoints/OKX/33,37",
		},
		{
			"http://proxy:8080/api",
			"https://example.com/other",
			"https://example.com/other", // not an NWS URL, unchanged
		},
		{
			"https://api.weather.gov",
			"https://api.weather.gov/points/40,-105",
			"https://api.weather.gov/points/40,-105", // same host, identity rewrite
		},
	}
	for _, tt := range tests {
		got := rewriteToBase(tt.base, tt.url)
		if got != tt.want {
			t.Errorf("rewriteToBase(%q, %q) = %q, want %q", tt.base, tt.url, got, tt.want)
		}
	}
}

func TestHaversineDistanceMiles(t *testing.T) {
	// New York to Los Angeles: ~2,451 miles
	d := haversineDistanceMiles(40.7128, -74.0060, 34.0522, -118.2437)
	if math.Abs(d-2451) > 10 {
		t.Errorf("NYC→LA = %.0f mi, want ~2451", d)
	}

	// Same point = 0.
	d = haversineDistanceMiles(40.0, -105.0, 40.0, -105.0)
	if d != 0 {
		t.Errorf("same point = %v, want 0", d)
	}
}
