package weather

import (
	"math"
	"testing"
	"time"
)

func TestComputeMoonPhaseKnownDates(t *testing.T) {
	tests := []struct {
		name       string
		date       time.Time
		wantName   string
		wantIllum  float64 // approximate, ±0.1
		illumDelta float64
	}{
		{
			name:       "known new moon epoch",
			date:       knownNewMoon,
			wantName:   "New Moon",
			wantIllum:  0.0,
			illumDelta: 0.05,
		},
		{
			name:       "full moon ~14.77 days after epoch",
			date:       knownNewMoon.Add(time.Duration(synodicPeriod/2*24*float64(time.Hour))),
			wantName:   "Full Moon",
			wantIllum:  1.0,
			illumDelta: 0.05,
		},
		{
			name:       "first quarter ~7.38 days after epoch",
			date:       knownNewMoon.Add(time.Duration(synodicPeriod/4*24*float64(time.Hour))),
			wantName:   "First Quarter",
			wantIllum:  0.5,
			illumDelta: 0.1,
		},
		{
			name:       "last quarter ~22.15 days after epoch",
			date:       knownNewMoon.Add(time.Duration(3*synodicPeriod/4*24*float64(time.Hour))),
			wantName:   "Last Quarter",
			wantIllum:  0.5,
			illumDelta: 0.1,
		},
		{
			name:       "one full cycle returns to new moon",
			date:       knownNewMoon.Add(time.Duration(synodicPeriod*24*float64(time.Hour))),
			wantName:   "New Moon",
			wantIllum:  0.0,
			illumDelta: 0.05,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp := ComputeMoonPhase(tt.date)
			if mp.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", mp.Name, tt.wantName)
			}
			if math.Abs(mp.Illumination-tt.wantIllum) > tt.illumDelta {
				t.Errorf("Illumination = %f, want ~%f (±%f)", mp.Illumination, tt.wantIllum, tt.illumDelta)
			}
		})
	}
}

func TestComputeMoonPhaseFields(t *testing.T) {
	mp := ComputeMoonPhase(knownNewMoon.Add(10 * 24 * time.Hour))

	if mp.Phase < 0 || mp.Phase > 1 {
		t.Errorf("Phase = %f, want 0–1", mp.Phase)
	}
	if mp.Illumination < 0 || mp.Illumination > 1 {
		t.Errorf("Illumination = %f, want 0–1", mp.Illumination)
	}
	if mp.AgeDays < 0 || mp.AgeDays > synodicPeriod {
		t.Errorf("AgeDays = %f, want 0–%f", mp.AgeDays, synodicPeriod)
	}
	if mp.Name == "" {
		t.Error("Name is empty")
	}
}

func TestPhaseNameAllEightPhases(t *testing.T) {
	// Sample one point from each of the eight phase sectors.
	tests := []struct {
		phase float64
		want  string
	}{
		{0.0, "New Moon"},
		{0.125, "Waxing Crescent"},
		{0.25, "First Quarter"},
		{0.375, "Waxing Gibbous"},
		{0.5, "Full Moon"},
		{0.625, "Waning Gibbous"},
		{0.75, "Last Quarter"},
		{0.875, "Waning Crescent"},
	}

	for _, tt := range tests {
		got := phaseName(tt.phase)
		if got != tt.want {
			t.Errorf("phaseName(%f) = %q, want %q", tt.phase, got, tt.want)
		}
	}
}

func TestPhaseNameBoundaries(t *testing.T) {
	// Test boundary values to ensure no gaps.
	tests := []struct {
		phase float64
		want  string
	}{
		{0.0, "New Moon"},
		{1.0/16 - 0.001, "New Moon"},
		{1.0 / 16, "Waxing Crescent"},
		{3.0/16 - 0.001, "Waxing Crescent"},
		{3.0 / 16, "First Quarter"},
		{5.0 / 16, "Waxing Gibbous"},
		{7.0 / 16, "Full Moon"},
		{9.0 / 16, "Waning Gibbous"},
		{11.0 / 16, "Last Quarter"},
		{13.0 / 16, "Waning Crescent"},
		{15.0 / 16, "New Moon"},       // wraps back
		{0.999, "New Moon"},            // near end of cycle
	}

	for _, tt := range tests {
		got := phaseName(tt.phase)
		if got != tt.want {
			t.Errorf("phaseName(%f) = %q, want %q", tt.phase, got, tt.want)
		}
	}
}

func TestComputeMoonPhaseNegativeAge(t *testing.T) {
	// Date before the epoch should still produce valid results.
	before := knownNewMoon.Add(-10 * 24 * time.Hour)
	mp := ComputeMoonPhase(before)

	if mp.AgeDays < 0 || mp.AgeDays > synodicPeriod {
		t.Errorf("AgeDays = %f for date before epoch, want 0–%f", mp.AgeDays, synodicPeriod)
	}
	if mp.Phase < 0 || mp.Phase > 1 {
		t.Errorf("Phase = %f for date before epoch, want 0–1", mp.Phase)
	}
}
