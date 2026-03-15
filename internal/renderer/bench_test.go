package renderer

import (
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~sbinet/gg"
	ann "github.com/zerohalo/weatherrupert/internal/announcements"
	"github.com/zerohalo/weatherrupert/internal/trivia"
	"github.com/zerohalo/weatherrupert/internal/weather"
)

func fp(v float64) *float64 { return &v }

func mockData() *weather.WeatherData {
	now := time.Date(2026, 3, 1, 14, 0, 0, 0, time.UTC)
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
			HeatIndexF:   fp(74),
			UpdatedAt:    now,
		},
		HourlyPeriods:  mockHourly(now),
		DailyPeriods:   mockDaily(now),
		MoonPhase:      weather.ComputeMoonPhase(now),
		PrecipTotal24h: 12.7,
		SnowTotal24h:   50.8,
		Alerts:         mockAlerts(now),
		TideData:       mockTideData(now),
	}
	return data
}

func mockHourly(base time.Time) []weather.ForecastPeriod {
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

func mockDaily(base time.Time) []weather.ForecastPeriod {
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

func mockAlerts(base time.Time) []weather.Alert {
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

func mockTideData(base time.Time) *weather.TideData {
	start := base.Truncate(time.Hour)
	preds := make([]weather.TidePrediction, 25)
	for i := range preds {
		t := start.Add(time.Duration(i) * time.Hour)
		level := 2.5 + 2.0*math.Sin(2*math.Pi*float64(i)/12.42)
		preds[i] = weather.TidePrediction{Time: t, Level: level}
	}
	hilo := []weather.TideHiLo{
		{Type: "H", Time: start.Add(3*time.Hour + 7*time.Minute), Level: 4.48},
		{Type: "L", Time: start.Add(9*time.Hour + 23*time.Minute), Level: 0.52},
		{Type: "H", Time: start.Add(15*time.Hour + 34*time.Minute), Level: 4.31},
		{Type: "L", Time: start.Add(21*time.Hour + 51*time.Minute), Level: 0.67},
	}
	return &weather.TideData{
		Station: weather.TideStation{
			ID: "8518750", Name: "The Battery, NY",
			Lat: 40.7006, Lon: -74.0142,
		},
		DistanceMiles: 12.3,
		Predictions:   preds,
		HiLo:          hilo,
	}
}

// TestRenderPreviewRace exercises concurrent slide advancement and preview
// rendering to verify the slideMu fix prevents data races. Run with -race.
func TestRenderPreviewRace(t *testing.T) {
	data := mockData()

	// Minimal mock weather client that returns canned data.
	wc := weather.NewClient("http://localhost", 40.0, -105.0, "Test, CO", 4, 120, nil, nil, nil)
	// Store data via the exported test helper.
	wc.StoreData(data)

	r := New(320, 240, 5, "test",
		func() time.Duration { return time.Second },
		wc, io.Discard, nil,
		func() []ann.Announcement { return nil }, func() time.Duration { return time.Second }, func() int { return 0 },
		func() []trivia.TriviaItem { return nil }, func() time.Duration { return time.Second }, func() int { return 0 }, func() bool { return false },
		func() bool { return false },
		func() bool { return false },
		false, false,
		nil,
	)

	var wg sync.WaitGroup
	// Goroutine 1: advance slides rapidly.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			r.slideMu.Lock()
			r.advanceSlide(data)
			r.slideSeq++
			r.slideStart = time.Now()
			r.slideMu.Unlock()
		}
	}()
	// Goroutine 2: call RenderPreview concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			r.RenderPreview()
		}
	}()
	wg.Wait()
}

func benchSlide(b *testing.B, slide SlideFunc, data *weather.WeatherData) {
	b.Helper()
	dc := gg.NewContext(1280, 720)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		slide(dc, data, 0, 0)
	}
}

func BenchmarkSlideCurrentConditions(b *testing.B) {
	data := mockData()
	benchSlide(b, NewSlideCurrentConditions(false, false, nil, nil, defaultFonts), data)
}

func BenchmarkSlideHourlyForecast(b *testing.B) {
	data := mockData()
	benchSlide(b, NewSlideHourlyForecast(false, false, nil, nil, defaultFonts), data)
}

func BenchmarkSlidePrecipitation(b *testing.B) {
	data := mockData()
	benchSlide(b, NewSlidePrecipitation(false, false, nil, nil, defaultFonts), data)
}

func BenchmarkSlideExtendedForecast(b *testing.B) {
	data := mockData()
	benchSlide(b, NewSlideExtendedForecast(false, false, nil, nil, defaultFonts), data)
}

func BenchmarkSlideMoonTides(b *testing.B) {
	data := mockData()
	benchSlide(b, NewSlideMoonTides(false, false, nil, defaultFonts), data)
}

func BenchmarkSlideMoonPhase(b *testing.B) {
	data := mockData()
	data.TideData = nil
	benchSlide(b, NewSlideMoonTides(false, false, nil, defaultFonts), data)
}

func BenchmarkSlideAlerts(b *testing.B) {
	data := mockData()
	benchSlide(b, NewSlideAlerts(false, nil, defaultFonts), data)
}

func BenchmarkSlideAnnouncements(b *testing.B) {
	data := mockData()
	anns := []ann.Announcement{
		{Text: "Winter Storm Warning in effect from Friday evening through Saturday afternoon"},
	}
	slide := NewSlideAnnouncements(
		func() []ann.Announcement { return anns },
		func() time.Duration { return 10 * time.Second },
		false,
		nil,
		defaultFonts,
	)
	benchSlide(b, slide, data)
}

func BenchmarkSlideTrivia(b *testing.B) {
	data := mockData()
	items := []trivia.TriviaItem{
		{Question: "What is the capital of France?", Answer: "Paris"},
	}
	slide := NewSlideTrivia(
		func() []trivia.TriviaItem { return items },
		func() time.Duration { return 20 * time.Second },
		func() bool { return false },
		false,
		nil,
		defaultFonts,
	)
	benchSlide(b, slide, data)
}

func BenchmarkDrawBackground(b *testing.B) {
	dc := gg.NewContext(1280, 720)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		drawBackground(dc, "LOCAL CONDITIONS", "Denver, CO", false, nil, defaultFonts)
	}
}
