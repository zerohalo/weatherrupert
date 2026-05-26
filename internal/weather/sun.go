package weather

import "time"

// ComputeSunData calculates sunrise, sunset, day length, solar noon, and
// golden hour for the given time and location. Uses the same solar position
// math as planet computations (sunAltitude). Returns nil for polar regions
// where the sun doesn't rise or set.
//
// t MUST be expressed in the location's timezone (e.g. now.In(loc)): the day
// boundaries are derived from t.Location(), and the morning/afternoon scan
// windows assume midnight falls during local night. Passing a mismatched
// timezone (server-local or UTC for a distant location) makes the scans miss
// the crossings and returns a spurious nil.
func ComputeSunData(t time.Time, lat, lon float64) *SunData {
	loc := t.Location()
	latRad := deg2rad(lat)
	date := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)

	// Find sunrise: scan from midnight to noon in 1-minute steps.
	var sunrise *time.Time
	for m := 0; m < 720; m++ {
		t0 := date.Add(time.Duration(m) * time.Minute)
		t1 := t0.Add(time.Minute)
		a0 := sunAltitude(t0, lat, lon, latRad)
		a1 := sunAltitude(t1, lat, lon, latRad)
		if a0 <= 0 && a1 > 0 {
			// Interpolate the crossing.
			frac := -a0 / (a1 - a0)
			sr := t0.Add(time.Duration(frac * float64(time.Minute)))
			sunrise = &sr
			break
		}
	}

	// Find sunset: scan from noon to midnight in 1-minute steps.
	var sunset *time.Time
	noon := date.Add(12 * time.Hour)
	for m := 0; m < 720; m++ {
		t0 := noon.Add(time.Duration(m) * time.Minute)
		t1 := t0.Add(time.Minute)
		a0 := sunAltitude(t0, lat, lon, latRad)
		a1 := sunAltitude(t1, lat, lon, latRad)
		if a0 > 0 && a1 <= 0 {
			frac := a0 / (a0 - a1)
			ss := t0.Add(time.Duration(frac * float64(time.Minute)))
			sunset = &ss
			break
		}
	}

	if sunrise == nil || sunset == nil {
		return nil // polar day/night
	}

	// Find solar noon: max altitude between sunrise and sunset.
	solarNoon := *sunrise
	maxAlt := -999.0
	for m := 0; m < int(sunset.Sub(*sunrise).Minutes()); m++ {
		t0 := sunrise.Add(time.Duration(m) * time.Minute)
		alt := sunAltitude(t0, lat, lon, latRad)
		if alt > maxAlt {
			maxAlt = alt
			solarNoon = t0
		}
	}

	// Find golden hour start: scan backwards from sunset to find when
	// sun altitude crosses ~6 degrees.
	goldenStart := sunset.Add(-30 * time.Minute) // fallback
	for m := 0; m < 120; m++ {
		t0 := sunset.Add(-time.Duration(m) * time.Minute)
		alt := sunAltitude(t0, lat, lon, latRad)
		if alt > 6.0 {
			goldenStart = t0
			break
		}
	}

	return &SunData{
		Sunrise:     *sunrise,
		Sunset:      *sunset,
		DayLength:   sunset.Sub(*sunrise),
		SolarNoon:   solarNoon,
		GoldenStart: goldenStart,
		GoldenEnd:   *sunset,
	}
}
