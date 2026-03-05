// Command screenshots renders each weather slide with mock data and saves PNGs.
package main

import (
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"time"

	"git.sr.ht/~sbinet/gg"
	"github.com/zerohalo/weatherrupert/internal/renderer"
	"github.com/zerohalo/weatherrupert/internal/trivia"
	"github.com/zerohalo/weatherrupert/internal/weather"
)

func main() {
	if err := os.MkdirAll("screenshots", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}

	now := time.Now()
	fp := func(v float64) *float64 { return &v }

	data := &weather.WeatherData{
		Location:  "Denver, CO",
		FetchedAt: now,
		Current: weather.CurrentConditions{
			Description:  "Partly Cloudy",
			TempF:        fp(72),
			DewpointF:    fp(52),
			Humidity:     fp(45),
			WindDir:      "NW",
			WindSpeedMph: fp(8),
			WindGustMph:  fp(15),
			PressureInHg: fp(30.12),
			VisibilityMi: fp(10),
			UpdatedAt:    now,
		},
		HourlyPeriods:  makeHourly(now),
		DailyPeriods:   makeDaily(now),
		MoonPhase:      weather.ComputeMoonPhase(now),
		PrecipTotal24h: 12.7,
		SnowTotal24h:   50.8,
	}

	slides := []struct {
		name  string
		slide renderer.SlideFunc
		tweak func(*weather.WeatherData)
	}{
		{"alerts", renderer.NewSlideAlerts(false, nil, nil), func(d *weather.WeatherData) {
			d.Alerts = makeAlerts(now)
		}},
		{"local-conditions", renderer.NewSlideCurrentConditions(false, false, nil, nil), func(d *weather.WeatherData) {
			d.Alerts = nil
		}},
		{"hourly-forecast", renderer.NewSlideHourlyForecast(false, false, nil, nil), nil},
		{"precipitation", renderer.NewSlidePrecipitation(false, false, nil, nil), nil},
		{"extended-forecast", renderer.NewSlideExtendedForecast(false, false, nil, nil), nil},
		{"moon-tides", renderer.NewSlideMoonTides(false, false, nil, nil), func(d *weather.WeatherData) {
			d.TideData = makeTideData(now)
		}},
		{"moon-phase", renderer.NewSlideMoonTides(false, false, nil, nil), func(d *weather.WeatherData) {
			d.TideData = nil
		}},
		{"night-sky", renderer.NewSlideNightSky(false, false, nil, nil, nil), func(d *weather.WeatherData) {
			d.Planets = makePlanetData(now)
		}},
		{"solar-weather", renderer.NewSlideSolarWeather(false, false, nil, nil), func(d *weather.WeatherData) {
			d.Solar = makeSolarData()
		}},
		{"satellite", renderer.NewSlideSatellite(false, nil, nil), func(d *weather.WeatherData) {
			d.SatelliteFrames = makeSatelliteFrames()
		}},
		{"radar", renderer.NewSlideRadar(false, nil, nil), func(d *weather.WeatherData) {
			d.RadarFrames = makeRadarFrames()
		}},
	}

	// Trivia slides — rendered with specific elapsed times for question/answer phases.
	triviaDur := 10 * time.Second
	getTrivDur := func() time.Duration { return triviaDur }
	mcItem := trivia.TriviaItem{
		Question: "What is on display in the Madame Tussaud's museum in London?",
		Answer:   "Wax sculptures",
		Choices:  []string{"Designer clothing", "Wax sculptures", "Unreleased film reels", "Vintage cars"},
	}
	tfItem := trivia.TriviaItem{
		Question: "The New York Subway is the oldest underground in the world.",
		Answer:   "False",
		Choices:  []string{"True", "False"},
	}

	triviaScreenshots := []struct {
		name    string
		item    trivia.TriviaItem
		elapsed time.Duration // 0 = question phase; 7s = answer phase
	}{
		{"trivia-mc-question", mcItem, 0},
		{"trivia-mc-answer", mcItem, 7 * time.Second},
		{"trivia-tf-question", tfItem, 0},
		{"trivia-tf-answer", tfItem, 7 * time.Second},
	}
	for _, ts := range triviaScreenshots {
		item := ts.item
		getItems := func() []trivia.TriviaItem { return []trivia.TriviaItem{item} }
		slide := renderer.NewSlideTrivia(getItems, getTrivDur, func() bool { return false }, false, nil, nil)

		dc := gg.NewContext(1280, 720)
		slide(dc, data, ts.elapsed, triviaDur)

		img, ok := dc.Image().(*image.RGBA)
		if !ok {
			fmt.Fprintf(os.Stderr, "unexpected image type for %s\n", ts.name)
			os.Exit(1)
		}
		path := fmt.Sprintf("screenshots/%s.png", ts.name)
		if err := savePNG(path, img); err != nil {
			fmt.Fprintf(os.Stderr, "save %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%dx%d)\n", path, img.Bounds().Dx(), img.Bounds().Dy())
	}

	for _, s := range slides {
		// Apply per-slide data tweaks.
		if s.tweak != nil {
			s.tweak(data)
		}

		dc := gg.NewContext(1280, 720)
		s.slide(dc, data, 0, 0)

		img, ok := dc.Image().(*image.RGBA)
		if !ok {
			fmt.Fprintf(os.Stderr, "unexpected image type for %s\n", s.name)
			os.Exit(1)
		}

		path := fmt.Sprintf("screenshots/%s.png", s.name)
		if err := savePNG(path, img); err != nil {
			fmt.Fprintf(os.Stderr, "save %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%dx%d)\n", path, img.Bounds().Dx(), img.Bounds().Dy())
	}

	// Reset TideData so it doesn't leak into a hypothetical later use.
	data.TideData = nil
}

func savePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func makeHourly(base time.Time) []weather.ForecastPeriod {
	conditions := []string{
		"Partly Cloudy", "Mostly Sunny", "Sunny", "Partly Cloudy",
		"Mostly Cloudy", "Slight Chance Rain", "Partly Cloudy", "Mostly Clear",
		"Clear", "Clear", "Partly Cloudy", "Mostly Cloudy",
	}
	temps := []int{72, 74, 76, 78, 77, 75, 73, 70, 68, 67, 66, 65}
	precipProbs := []int{5, 10, 0, 15, 40, 65, 80, 55, 30, 10, 5, 0}

	periods := make([]weather.ForecastPeriod, 12)
	for i := range periods {
		t := base.Add(time.Duration(i) * time.Hour)
		prob := precipProbs[i]
		periods[i] = weather.ForecastPeriod{
			Number:          i + 1,
			Name:            "",
			StartTime:       t.Format(time.RFC3339),
			EndTime:         t.Add(time.Hour).Format(time.RFC3339),
			IsDaytime:       t.Hour() >= 6 && t.Hour() < 20,
			Temperature:     temps[i],
			TemperatureUnit: "F",
			WindSpeed:       "8 mph",
			WindDirection:   "NW",
			ShortForecast:   conditions[i],
		}
		periods[i].ProbabilityOfPrecipitation.Value = &prob
	}
	return periods
}

func makeDaily(base time.Time) []weather.ForecastPeriod {
	type dayNight struct {
		dayName, dayFc     string
		dayTemp            int
		nightName, nightFc string
		nightTemp          int
	}
	forecasts := []dayNight{
		{"Today", "Partly Cloudy", 78, "Tonight", "Mostly Clear", 55},
		{"Monday", "Sunny", 82, "Monday Night", "Clear", 58},
		{"Tuesday", "Mostly Sunny", 80, "Tuesday Night", "Partly Cloudy", 56},
		{"Wednesday", "Chance Rain", 71, "Wednesday Night", "Showers Likely", 50},
		{"Thursday", "Mostly Cloudy", 68, "Thursday Night", "Partly Cloudy", 48},
		{"Friday", "Sunny", 75, "Friday Night", "Mostly Clear", 52},
		{"Saturday", "Partly Cloudy", 77, "Saturday Night", "Mostly Cloudy", 54},
	}

	periods := make([]weather.ForecastPeriod, 0, 14)
	for i, f := range forecasts {
		dayStart := base.AddDate(0, 0, i).Truncate(24 * time.Hour).Add(6 * time.Hour)
		nightStart := dayStart.Add(12 * time.Hour)

		periods = append(periods, weather.ForecastPeriod{
			Number:          len(periods) + 1,
			Name:            f.dayName,
			StartTime:       dayStart.Format(time.RFC3339),
			EndTime:         nightStart.Format(time.RFC3339),
			IsDaytime:       true,
			Temperature:     f.dayTemp,
			TemperatureUnit: "F",
			WindSpeed:       "10 mph",
			WindDirection:   "W",
			ShortForecast:   f.dayFc,
		})
		periods = append(periods, weather.ForecastPeriod{
			Number:          len(periods) + 1,
			Name:            f.nightName,
			StartTime:       nightStart.Format(time.RFC3339),
			EndTime:         nightStart.Add(12 * time.Hour).Format(time.RFC3339),
			IsDaytime:       false,
			Temperature:     f.nightTemp,
			TemperatureUnit: "F",
			WindSpeed:       "5 mph",
			WindDirection:   "NW",
			ShortForecast:   f.nightFc,
		})
	}
	return periods
}

func makeAlerts(base time.Time) []weather.Alert {
	return []weather.Alert{
		{
			Event:    "Winter Storm Warning",
			Severity: "Severe",
			Headline: "Winter Storm Warning in effect from Friday evening through Saturday afternoon",
			Onset:    base.Add(12 * time.Hour),
			Expires:  base.Add(36 * time.Hour),
		},
		{
			Event:    "Wind Advisory",
			Severity: "Moderate",
			Headline: "Wind Advisory in effect until Friday 6:00 PM MST",
			Onset:    base,
			Expires:  base.Add(8 * time.Hour),
		},
	}
}

func makePlanetData(base time.Time) *weather.PlanetData {
	day := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location())
	rise := day.Add(5*time.Hour + 42*time.Minute)
	set := day.Add(15*time.Hour + 18*time.Minute)
	transit := day.Add(10*time.Hour + 30*time.Minute)
	marsRise := day.Add(22*time.Hour + 15*time.Minute)
	sunset := day.Add(17*time.Hour + 48*time.Minute)
	sunsetPlanets := []weather.PlanetInfo{
		{Name: "Mercury", Altitude: -15, Azimuth: 250, Magnitude: 0.3, RiseTime: &rise, Compass: "WSW", IsUp: false},
		{Name: "Venus", Altitude: 32, Azimuth: 225, Magnitude: -4.1, RiseTime: &rise, SetTime: &set, TransitTime: &transit, Compass: "SW", IsUp: true},
		{Name: "Mars", Altitude: -5, Azimuth: 60, Magnitude: 1.2, RiseTime: &marsRise, Compass: "ENE", IsUp: false},
		{Name: "Jupiter", Altitude: 58, Azimuth: 180, Magnitude: -2.3, Compass: "S", IsUp: true},
		{Name: "Saturn", Altitude: 15, Azimuth: 245, Magnitude: 0.8, Compass: "WSW", IsUp: true},
	}
	// Live positions differ slightly (daytime, most below horizon).
	livePlanets := []weather.PlanetInfo{
		{Name: "Mercury", Altitude: -25, Azimuth: 80, Magnitude: 0.3, RiseTime: &rise, Compass: "E", IsUp: false},
		{Name: "Venus", Altitude: -20, Azimuth: 75, Magnitude: -4.1, RiseTime: &rise, SetTime: &set, TransitTime: &transit, Compass: "E", IsUp: false},
		{Name: "Mars", Altitude: -10, Azimuth: 90, Magnitude: 1.2, RiseTime: &marsRise, Compass: "E", IsUp: false},
		{Name: "Jupiter", Altitude: 12, Azimuth: 300, Magnitude: -2.3, Compass: "WNW", IsUp: true},
		{Name: "Saturn", Altitude: -30, Azimuth: 70, Magnitude: 0.8, Compass: "ENE", IsUp: false},
	}
	return &weather.PlanetData{
		LivePlanets:   livePlanets,
		SunsetPlanets: sunsetPlanets,
		ComputedAt:    base,
		SunsetTime:    &sunset,
		BeforeSunset:  true,
	}
}

func makeTideData(base time.Time) *weather.TideData {
	start := base.Truncate(time.Hour)
	preds := make([]weather.TidePrediction, 25)
	for i := range preds {
		t := start.Add(time.Duration(i) * time.Hour)
		// Semidiurnal tide: two highs and two lows per day.
		// period ~12.42 hours → angular freq = 2π/12.42
		level := 2.5 + 2.0*math.Sin(2*math.Pi*float64(i)/12.42)
		preds[i] = weather.TidePrediction{
			Time:  t,
			Level: level,
		}
	}
	// Exact high/low tide events with realistic non-round times.
	hilo := []weather.TideHiLo{
		{Type: "H", Time: start.Add(3*time.Hour + 7*time.Minute), Level: 4.48},
		{Type: "L", Time: start.Add(9*time.Hour + 23*time.Minute), Level: 0.52},
		{Type: "H", Time: start.Add(15*time.Hour + 34*time.Minute), Level: 4.31},
		{Type: "L", Time: start.Add(21*time.Hour + 51*time.Minute), Level: 0.67},
	}
	return &weather.TideData{
		Station: weather.TideStation{
			ID:   "8518750",
			Name: "The Battery, NY",
			Lat:  40.7006,
			Lon:  -74.0142,
		},
		DistanceMiles: 12.3,
		Predictions:   preds,
		HiLo:          hilo,
	}
}

func makeRadarFrames() [][]byte {
	const (
		lat         = 39.7392
		lon         = -104.9903
		radius      = 120.0
		milesPerDeg = 69.0
	)
	latSpan := radius / milesPerDeg
	lonSpan := radius / (milesPerDeg * math.Cos(lat*math.Pi/180))

	now := time.Now().UTC()
	numFrames := 4
	frames := make([][]byte, 0, numFrames)
	for i := 0; i < numFrames; i++ {
		offset := time.Duration(numFrames-1-i) * time.Hour
		t := now.Add(-offset).Round(5 * time.Minute)

		u := fmt.Sprintf("https://mesonet.agron.iastate.edu/GIS/radmap.php?layers[]=n0q&layers[]=uscounties&width=1280&height=610&bbox=%.4f,%.4f,%.4f,%.4f&ts=%s",
			lon-lonSpan, lat-latSpan, lon+lonSpan, lat+latSpan, t.Format("200601021504"))
		data := fetchURL(u)
		if data != nil {
			frames = append(frames, data)
		}
	}
	return frames
}

func makeSatelliteFrames() [][]byte {
	// Denver coordinates, same bounding box math as client.go.
	const (
		lat         = 39.7392
		lon         = -104.9903
		radius      = 120.0
		milesPerDeg = 69.0
	)
	latSpan := radius / milesPerDeg
	lonSpan := radius / (milesPerDeg * math.Cos(lat*math.Pi/180))

	now := time.Now().UTC()
	numFrames := 4
	frames := make([][]byte, 0, numFrames)
	for i := 0; i < numFrames; i++ {
		offset := time.Duration(numFrames-1-i) * time.Hour
		t := now.Add(-offset).Round(5 * time.Minute)

		u := fmt.Sprintf("https://mesonet.agron.iastate.edu/GIS/radmap.php?layers[]=goes&goes_product=IR&layers[]=uscounties&width=1280&height=610&bbox=%.4f,%.4f,%.4f,%.4f&ts=%s",
			lon-lonSpan, lat-latSpan, lon+lonSpan, lat+latSpan, t.Format("200601021504"))
		data := fetchURL(u)
		if data != nil {
			frames = append(frames, data)
		}
	}
	return frames
}

func makeSolarData() *weather.SolarData {
	sd := &weather.SolarData{
		KpIndex:      2.3,
		XRayFlux:     "C2.1",
		RadioScale:   0,
		SolarScale:   0,
		GeomagScale:  0,
		WindSpeedKms: 425,
		FetchedAt:    time.Now(),
	}
	// Fetch real solar images for the screenshot (best-effort, SDAC mirror first).
	sd.SunspotImage = fetchURL("https://umbra.nascom.nasa.gov/images/latest_hmi_igram.gif")
	if sd.SunspotImage == nil {
		sd.SunspotImage = fetchURL("https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_HMIIC.jpg")
	}
	sd.CoronaImage = fetchURL("https://umbra.nascom.nasa.gov/images/latest_aia_304.gif")
	if sd.CoronaImage == nil {
		sd.CoronaImage = fetchURL("https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_0304.jpg")
	}
	return sd
}

func fetchURL(url string) []byte {
	client := &http.Client{Timeout: 60 * time.Second}
	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := client.Get(url)
		if err != nil {
			log.Printf("screenshot: fetch %s attempt %d/%d failed: %v", url, attempt, maxRetries, err)
			if attempt < maxRetries {
				time.Sleep(2 * time.Second)
			}
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("screenshot: read %s attempt %d/%d failed: %v", url, attempt, maxRetries, err)
			if attempt < maxRetries {
				time.Sleep(2 * time.Second)
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			log.Printf("screenshot: fetch %s attempt %d/%d returned HTTP %d", url, attempt, maxRetries, resp.StatusCode)
			if attempt < maxRetries {
				time.Sleep(2 * time.Second)
			}
			continue
		}
		return data
	}
	return nil
}
