package weather

import (
	"math"
	"time"
)

// MoonPhase holds the computed lunar phase for a given instant.
type MoonPhase struct {
	Name         string  // e.g. "Full Moon", "Waxing Crescent"
	Illumination float64 // 0–1 fraction of disc illuminated
	Phase        float64 // 0–1 position in the synodic cycle (0 = new moon)
	AgeDays      float64 // days since the most recent new moon
}

// synodicPeriod is the mean length of the lunar synodic month in days.
const synodicPeriod = 29.53059

// knownNewMoon is the epoch of a known new moon (Jan 6, 2000 18:14 UTC).
var knownNewMoon = time.Date(2000, 1, 6, 18, 14, 0, 0, time.UTC)

// ComputeMoonPhase returns the moon phase for the given time.
func ComputeMoonPhase(t time.Time) MoonPhase {
	daysSinceEpoch := t.Sub(knownNewMoon).Hours() / 24.0
	age := math.Mod(daysSinceEpoch, synodicPeriod)
	if age < 0 {
		age += synodicPeriod
	}

	phase := age / synodicPeriod // 0–1
	illumination := 0.5 * (1 - math.Cos(2*math.Pi*phase))

	name := phaseName(phase)

	return MoonPhase{
		Name:         name,
		Illumination: illumination,
		Phase:        phase,
		AgeDays:      age,
	}
}

// phaseName maps a 0–1 phase value to one of eight traditional phase names.
func phaseName(phase float64) string {
	// Each phase occupies 1/8 of the cycle.
	switch {
	case phase < 1.0/16 || phase >= 15.0/16:
		return "New Moon"
	case phase < 3.0/16:
		return "Waxing Crescent"
	case phase < 5.0/16:
		return "First Quarter"
	case phase < 7.0/16:
		return "Waxing Gibbous"
	case phase < 9.0/16:
		return "Full Moon"
	case phase < 11.0/16:
		return "Waning Gibbous"
	case phase < 13.0/16:
		return "Last Quarter"
	default:
		return "Waning Crescent"
	}
}
