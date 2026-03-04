package weather

import (
	"math"
	"sort"
	"time"
)

// ComputePlanets returns positions of the five naked-eye planets for the given
// time and observer location. Uses the Paul Schlyter / stjarnhimlen.se
// simplified algorithm — all orbital constants are inline; no external data
// files or API calls required.
//
// Both live (current) and sunset positions are computed so the renderer can
// switch between them without waiting for a weather refresh. Rise/set/transit
// times always cover the full local day.
func ComputePlanets(t time.Time, lat, lon float64) *PlanetData {
	latRad := deg2rad(lat)

	// Live positions at current time.
	livePlanets := computePlanetPositions(t, lat, lon, latRad)

	// Rise/set/transit times use the full local day.
	d := dayNumber(t)
	sunLon, sunR := sunPosition(d)
	oblEcl := deg2rad(23.4393 - 3.563e-7*d)
	computeRiseSetTransit(livePlanets, d, t, lat, lon, latRad, sunLon, sunR, oblEcl)

	// Compute sunset and (if before sunset) sunset positions.
	sunset := computeSunset(t, lat, lon)
	beforeSunset := sunset != nil && t.Before(*sunset)

	var sunsetPlanets []PlanetInfo
	if beforeSunset {
		sunsetPlanets = computePlanetPositions(*sunset, lat, lon, latRad)
		// Copy rise/set/transit from live (same day).
		for i := range sunsetPlanets {
			sunsetPlanets[i].RiseTime = livePlanets[i].RiseTime
			sunsetPlanets[i].SetTime = livePlanets[i].SetTime
			sunsetPlanets[i].TransitTime = livePlanets[i].TransitTime
		}
	}

	return &PlanetData{
		LivePlanets:   livePlanets,
		SunsetPlanets: sunsetPlanets,
		ComputedAt:    t,
		SunsetTime:    sunset,
		BeforeSunset:  beforeSunset,
	}
}

// computePlanetPositions returns altitude/azimuth/magnitude for all 5 planets
// at the given time and location.
func computePlanetPositions(t time.Time, lat, lon, latRad float64) []PlanetInfo {
	d := dayNumber(t)
	sunLon, sunR := sunPosition(d)
	oblEcl := deg2rad(23.4393 - 3.563e-7*d)

	planets := make([]PlanetInfo, 5)
	names := [5]string{"Mercury", "Venus", "Mars", "Jupiter", "Saturn"}
	for i, name := range names {
		elem := orbitalElements(name, d)

		// Solve Kepler's equation for eccentric anomaly.
		E := solveKepler(deg2rad(elem.M), elem.e)

		// Heliocentric rectangular ecliptic coordinates.
		xv := elem.a * (math.Cos(E) - elem.e)
		yv := elem.a * math.Sqrt(1-elem.e*elem.e) * math.Sin(E)
		v := math.Atan2(yv, xv)   // true anomaly
		r := math.Sqrt(xv*xv + yv*yv) // distance from Sun

		NRad := deg2rad(elem.N)
		iRad := deg2rad(elem.i)
		wRad := deg2rad(elem.w)

		// Heliocentric ecliptic coordinates.
		xh := r * (math.Cos(NRad)*math.Cos(v+wRad) - math.Sin(NRad)*math.Sin(v+wRad)*math.Cos(iRad))
		yh := r * (math.Sin(NRad)*math.Cos(v+wRad) + math.Cos(NRad)*math.Sin(v+wRad)*math.Cos(iRad))
		zh := r * math.Sin(v+wRad) * math.Sin(iRad)

		// Apply perturbation corrections for Jupiter and Saturn.
		xh, yh, zh = applyPerturbations(name, d, xh, yh, zh)

		// Geocentric ecliptic coordinates.
		sunLonRad := deg2rad(sunLon)
		xs := sunR * math.Cos(sunLonRad)
		ys := sunR * math.Sin(sunLonRad)

		xg := xh + xs
		yg := yh + ys
		zg := zh

		// Ecliptic to equatorial.
		xe := xg
		ye := yg*math.Cos(oblEcl) - zg*math.Sin(oblEcl)
		ze := yg*math.Sin(oblEcl) + zg*math.Cos(oblEcl)

		// Right ascension and declination.
		ra := math.Atan2(ye, xe)
		dec := math.Atan2(ze, math.Sqrt(xe*xe+ye*ye))

		// Horizontal coordinates.
		alt, az := equatorialToHorizontal(ra, dec, d, lat, lon, latRad)

		// Distance from Earth for magnitude calc.
		distEarth := math.Sqrt(xg*xg + yg*yg + zg*zg)

		// Phase angle.
		phaseAngle := phaseAngleDeg(r, distEarth, sunR)

		mag := visualMagnitude(name, r, distEarth, phaseAngle)
		compass := azimuthToCompass(az)

		planets[i] = PlanetInfo{
			Name:      name,
			Altitude:  alt,
			Azimuth:   az,
			Magnitude: mag,
			Compass:   compass,
			IsUp:      alt > 0,
		}
	}
	return planets
}

// computeSunset finds today's sunset time by sampling the sun's altitude at
// 5-minute intervals from noon onward and detecting the horizon crossing.
// Returns nil if the sun doesn't set (polar day/night).
func computeSunset(t time.Time, lat, lon float64) *time.Time {
	loc := t.Location()
	noon := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
	latRad := deg2rad(lat)

	prevAlt := sunAltitude(noon, lat, lon, latRad)
	for step := 1; step <= 144; step++ { // 12h in 5-min steps
		sample := noon.Add(time.Duration(step) * 5 * time.Minute)
		alt := sunAltitude(sample, lat, lon, latRad)
		if prevAlt > 0 && alt <= 0 {
			// Interpolate the crossing.
			frac := prevAlt / (prevAlt - alt)
			sunset := sample.Add(-5 * time.Minute).Add(time.Duration(frac*5) * time.Minute)
			return &sunset
		}
		prevAlt = alt
	}
	return nil
}

// sunAltitude returns the sun's altitude in degrees for a given time/location.
func sunAltitude(t time.Time, lat, lon, latRad float64) float64 {
	d := dayNumber(t)
	sunLon, _ := sunPosition(d)
	oblEcl := deg2rad(23.4393 - 3.563e-7*d)

	// Sun's ecliptic coords → equatorial.
	sunLonRad := deg2rad(sunLon)
	xe := math.Cos(sunLonRad)
	ye := math.Cos(oblEcl) * math.Sin(sunLonRad)
	ze := math.Sin(oblEcl) * math.Sin(sunLonRad)

	ra := math.Atan2(ye, xe)
	dec := math.Atan2(ze, math.Sqrt(xe*xe+ye*ye))

	alt, _ := equatorialToHorizontal(ra, dec, d, lat, lon, latRad)
	return alt
}

// orbitalElem holds Keplerian orbital elements at a given epoch.
type orbitalElem struct {
	N float64 // longitude of ascending node (deg)
	i float64 // inclination (deg)
	w float64 // argument of perihelion (deg)
	a float64 // semi-major axis (AU)
	e float64 // eccentricity
	M float64 // mean anomaly (deg)
}

// orbitalElements returns Keplerian elements for a planet at day number d.
// Constants from Paul Schlyter's stjarnhimlen.se tables.
func orbitalElements(name string, d float64) orbitalElem {
	switch name {
	case "Mercury":
		return orbitalElem{
			N: rev(48.3313 + 3.24587e-5*d),
			i: 7.0047 + 5.00e-8*d,
			w: rev(29.1241 + 1.01444e-5*d),
			a: 0.387098,
			e: 0.205635 + 5.59e-10*d,
			M: rev(168.6562 + 4.0923344368*d),
		}
	case "Venus":
		return orbitalElem{
			N: rev(76.6799 + 2.46590e-5*d),
			i: 3.3946 + 2.75e-8*d,
			w: rev(54.8910 + 1.38374e-5*d),
			a: 0.723330,
			e: 0.006773 - 1.302e-9*d,
			M: rev(48.0052 + 1.6021302244*d),
		}
	case "Mars":
		return orbitalElem{
			N: rev(49.5574 + 2.11081e-5*d),
			i: 1.8497 - 1.78e-8*d,
			w: rev(286.5016 + 2.92961e-5*d),
			a: 1.523688,
			e: 0.093405 + 2.516e-9*d,
			M: rev(18.6021 + 0.5240207766*d),
		}
	case "Jupiter":
		return orbitalElem{
			N: rev(100.4542 + 2.76854e-5*d),
			i: 1.3030 - 1.557e-7*d,
			w: rev(273.8777 + 1.64505e-5*d),
			a: 5.20256,
			e: 0.048498 + 4.469e-9*d,
			M: rev(19.8950 + 0.0830853001*d),
		}
	case "Saturn":
		return orbitalElem{
			N: rev(113.6634 + 2.38980e-5*d),
			i: 2.4886 - 1.081e-7*d,
			w: rev(339.3939 + 2.97661e-5*d),
			a: 9.55475,
			e: 0.055546 - 9.499e-9*d,
			M: rev(316.9670 + 0.0334442282*d),
		}
	}
	return orbitalElem{}
}

// dayNumber returns days since the epoch 1999 Dec 31.0 UT (J2000 - 0.5).
func dayNumber(t time.Time) float64 {
	utc := t.UTC()
	y := utc.Year()
	m := int(utc.Month())
	D := utc.Day()
	ut := float64(utc.Hour()) + float64(utc.Minute())/60.0 + float64(utc.Second())/3600.0
	d := 367*y - 7*(y+(m+9)/12)/4 + 275*m/9 + D - 730530
	return float64(d) + ut/24.0
}

// sunPosition returns the Sun's ecliptic longitude (deg) and distance (AU).
func sunPosition(d float64) (float64, float64) {
	w := rev(282.9404 + 4.70935e-5*d)
	e := 0.016709 - 1.151e-9*d
	M := rev(356.0470 + 0.9856002585*d)
	MRad := deg2rad(M)

	E := MRad + e*math.Sin(MRad)*(1+e*math.Cos(MRad))
	xv := math.Cos(E) - e
	yv := math.Sqrt(1-e*e) * math.Sin(E)
	v := rad2deg(math.Atan2(yv, xv))
	r := math.Sqrt(xv*xv + yv*yv)

	lon := rev(v + w)
	return lon, r
}

// solveKepler iteratively solves Kepler's equation M = E - e*sin(E).
func solveKepler(M, e float64) float64 {
	E := M + e*math.Sin(M)*(1+e*math.Cos(M))
	for i := 0; i < 20; i++ {
		dE := (E - e*math.Sin(E) - M) / (1 - e*math.Cos(E))
		E -= dE
		if math.Abs(dE) < 1e-12 {
			break
		}
	}
	return E
}

// equatorialToHorizontal converts RA/Dec to altitude/azimuth for an observer.
func equatorialToHorizontal(ra, dec, d, lat, lon, latRad float64) (alt, az float64) {
	// Local sidereal time.
	lst := localSiderealTime(d, lon)
	ha := deg2rad(lst) - ra // hour angle

	sinLat := math.Sin(latRad)
	cosLat := math.Cos(latRad)
	sinDec := math.Sin(dec)
	cosDec := math.Cos(dec)
	cosHA := math.Cos(ha)
	sinHA := math.Sin(ha)

	sinAlt := sinDec*sinLat + cosDec*cosLat*cosHA
	alt = rad2deg(math.Asin(sinAlt))

	cosAlt := math.Cos(math.Asin(sinAlt))
	cosAz := (sinDec - sinLat*sinAlt) / (cosLat * cosAlt)
	cosAz = math.Max(-1, math.Min(1, cosAz)) // clamp
	az = rad2deg(math.Acos(cosAz))
	if sinHA > 0 {
		az = 360 - az
	}
	return alt, az
}

// localSiderealTime returns the local sidereal time in degrees.
// Uses the Schlyter formula compatible with the dayNumber epoch (d=0 at 1999 Dec 31.0 UT).
func localSiderealTime(d, lon float64) float64 {
	// Sun's mean longitude (Schlyter epoch).
	w := rev(282.9404 + 4.70935e-5*d)
	M := rev(356.0470 + 0.9856002585*d)
	L := rev(w + M) // Sun's mean longitude

	// GMST at 0h UT = L + 180°.
	// Fractional day gives the UT offset: d mod 1 * 360.98564736629°.
	ut := math.Mod(d, 1.0)
	if ut < 0 {
		ut += 1.0
	}
	gmst := rev(L + 180 + ut*360.98564736629)
	return rev(gmst + lon)
}

// applyPerturbations applies Jupiter/Saturn mutual perturbation corrections.
func applyPerturbations(name string, d float64, xh, yh, zh float64) (float64, float64, float64) {
	if name != "Jupiter" && name != "Saturn" {
		return xh, yh, zh
	}

	Mj := deg2rad(rev(19.8950 + 0.0830853001*d))
	Ms := deg2rad(rev(316.9670 + 0.0334442282*d))

	if name == "Jupiter" {
		lonCorr := -0.332*math.Sin(2*Mj-5*Ms-67.6*math.Pi/180) -
			0.056*math.Sin(2*Mj-2*Ms+21*math.Pi/180) +
			0.042*math.Sin(3*Mj-5*Ms+21*math.Pi/180) -
			0.036*math.Sin(Mj-2*Ms) +
			0.022*math.Cos(Mj-Ms) +
			0.023*math.Sin(2*Mj-3*Ms+52*math.Pi/180) -
			0.016*math.Sin(Mj-5*Ms-69*math.Pi/180)
		lonCorr = deg2rad(lonCorr)

		// Convert to ecliptic longitude perturbation in Cartesian.
		eclLon := math.Atan2(yh, xh)
		r := math.Sqrt(xh*xh + yh*yh + zh*zh)
		eclLat := math.Asin(zh / r)
		eclLon += lonCorr

		xh = r * math.Cos(eclLat) * math.Cos(eclLon)
		yh = r * math.Cos(eclLat) * math.Sin(eclLon)
		zh = r * math.Sin(eclLat)
	} else { // Saturn
		lonCorr := 0.812*math.Sin(2*Mj-5*Ms-67.6*math.Pi/180) -
			0.229*math.Cos(2*Mj-4*Ms-2*math.Pi/180) +
			0.119*math.Sin(Mj-2*Ms-3*math.Pi/180) +
			0.046*math.Sin(2*Mj-6*Ms-69*math.Pi/180) +
			0.014*math.Sin(Mj-3*Ms+32*math.Pi/180)
		latCorr := -0.020*math.Cos(2*Mj-4*Ms-2*math.Pi/180) +
			0.018*math.Sin(2*Mj-6*Ms-49*math.Pi/180)
		lonCorr = deg2rad(lonCorr)
		latCorr = deg2rad(latCorr)

		eclLon := math.Atan2(yh, xh)
		r := math.Sqrt(xh*xh + yh*yh + zh*zh)
		eclLat := math.Asin(zh / r)
		eclLon += lonCorr
		eclLat += latCorr

		xh = r * math.Cos(eclLat) * math.Cos(eclLon)
		yh = r * math.Cos(eclLat) * math.Sin(eclLon)
		zh = r * math.Sin(eclLat)
	}
	return xh, yh, zh
}

// phaseAngleDeg returns the phase angle in degrees given distances.
func phaseAngleDeg(rSun, rEarth, rSunEarth float64) float64 {
	cosPhase := (rSun*rSun + rEarth*rEarth - rSunEarth*rSunEarth) / (2 * rSun * rEarth)
	cosPhase = math.Max(-1, math.Min(1, cosPhase))
	return rad2deg(math.Acos(cosPhase))
}

// visualMagnitude returns simplified visual magnitude from distance and phase.
func visualMagnitude(name string, r, delta, phaseAngle float64) float64 {
	i := phaseAngle
	// Base magnitude at 1 AU from Sun and Earth, 0° phase.
	// Formulas from the Astronomical Almanac (simplified).
	switch name {
	case "Mercury":
		return -0.36 + 5*math.Log10(r*delta) + 0.027*i + 2.2e-13*math.Pow(i, 6)
	case "Venus":
		return -4.34 + 5*math.Log10(r*delta) + 0.013*i + 4.2e-7*math.Pow(i, 3)
	case "Mars":
		return -1.51 + 5*math.Log10(r*delta) + 0.016*i
	case "Jupiter":
		return -9.25 + 5*math.Log10(r*delta) + 0.014*i
	case "Saturn":
		// Simplified — ignores ring tilt contribution.
		return -8.95 + 5*math.Log10(r*delta) + 0.044*i
	}
	return 0
}

// computeRiseSetTransit finds rise/set/transit times by sampling altitude
// at 10-minute intervals over the local day and detecting zero-crossings.
func computeRiseSetTransit(planets []PlanetInfo, d float64, t time.Time, lat, lon, latRad float64, sunLon, sunR float64, oblEcl float64) {
	// Start of local day (midnight).
	loc := t.Location()
	dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)

	for idx := range planets {
		name := planets[idx].Name

		var riseTime, setTime, transitTime *time.Time
		maxAlt := -999.0
		var maxAltTime time.Time
		prevAlt := 0.0

		for step := 0; step <= 144; step++ { // 24h * 6 steps/hour = 144
			sampleTime := dayStart.Add(time.Duration(step) * 10 * time.Minute)
			sd := dayNumber(sampleTime)
			sLon, sR := sunPosition(sd)
			obl := deg2rad(23.4393 - 3.563e-7*sd)

			alt := planetAltitude(name, sd, lat, lon, latRad, sLon, sR, obl)

			if step > 0 {
				if prevAlt <= 0 && alt > 0 && riseTime == nil {
					// Interpolate rise time.
					frac := -prevAlt / (alt - prevAlt)
					rt := sampleTime.Add(-10 * time.Minute).Add(time.Duration(frac*10) * time.Minute)
					riseTime = &rt
				}
				if prevAlt > 0 && alt <= 0 && setTime == nil {
					frac := prevAlt / (prevAlt - alt)
					st := sampleTime.Add(-10 * time.Minute).Add(time.Duration(frac*10) * time.Minute)
					setTime = &st
				}
			}

			if alt > maxAlt {
				maxAlt = alt
				maxAltTime = sampleTime
			}
			prevAlt = alt
		}

		if maxAlt > 0 {
			tt := maxAltTime
			transitTime = &tt
		}

		planets[idx].RiseTime = riseTime
		planets[idx].SetTime = setTime
		planets[idx].TransitTime = transitTime
	}
}

// planetAltitude computes the altitude of a planet at a specific time.
func planetAltitude(name string, d, lat, lon, latRad float64, sunLon, sunR, oblEcl float64) float64 {
	elem := orbitalElements(name, d)
	E := solveKepler(deg2rad(elem.M), elem.e)

	xv := elem.a * (math.Cos(E) - elem.e)
	yv := elem.a * math.Sqrt(1-elem.e*elem.e) * math.Sin(E)
	v := math.Atan2(yv, xv)
	r := math.Sqrt(xv*xv + yv*yv)

	NRad := deg2rad(elem.N)
	iRad := deg2rad(elem.i)
	wRad := deg2rad(elem.w)

	xh := r * (math.Cos(NRad)*math.Cos(v+wRad) - math.Sin(NRad)*math.Sin(v+wRad)*math.Cos(iRad))
	yh := r * (math.Sin(NRad)*math.Cos(v+wRad) + math.Cos(NRad)*math.Sin(v+wRad)*math.Cos(iRad))
	zh := r * math.Sin(v+wRad) * math.Sin(iRad)

	xh, yh, zh = applyPerturbations(name, d, xh, yh, zh)

	sunLonRad := deg2rad(sunLon)
	xg := xh + sunR*math.Cos(sunLonRad)
	yg := yh + sunR*math.Sin(sunLonRad)
	zg := zh

	xe := xg
	ye := yg*math.Cos(oblEcl) - zg*math.Sin(oblEcl)
	ze := yg*math.Sin(oblEcl) + zg*math.Cos(oblEcl)

	ra := math.Atan2(ye, xe)
	dec := math.Atan2(ze, math.Sqrt(xe*xe+ye*ye))

	alt, _ := equatorialToHorizontal(ra, dec, d, lat, lon, latRad)
	return alt
}

// azimuthToCompass converts azimuth degrees to 8-point compass direction.
func azimuthToCompass(az float64) string {
	dirs := [8]string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	idx := int(math.Round(az/45.0)) % 8
	if idx < 0 {
		idx += 8
	}
	return dirs[idx]
}

// Utility functions.

func deg2rad(d float64) float64 { return d * math.Pi / 180 }
func rad2deg(r float64) float64 { return r * 180 / math.Pi }

// rev normalizes an angle to 0–360 degrees.
func rev(x float64) float64 {
	r := math.Mod(x, 360)
	if r < 0 {
		r += 360
	}
	return r
}

// SortPlanetsByAltitude sorts planets by altitude descending.
func SortPlanetsByAltitude(planets []PlanetInfo) []PlanetInfo {
	sorted := make([]PlanetInfo, len(planets))
	copy(sorted, planets)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Altitude > sorted[j].Altitude
	})
	return sorted
}
