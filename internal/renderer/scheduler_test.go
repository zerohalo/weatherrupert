package renderer

import (
	"testing"
	"time"

	"git.sr.ht/~sbinet/gg"
	"github.com/zerohalo/weatherrupert/internal/weather"
)

// noopSlide is a no-op SlideFunc for scheduler testing.
func noopSlide(*gg.Context, *weather.WeatherData, time.Duration, time.Duration) time.Duration {
	return 0
}

// newTestRenderer creates a minimal Renderer with named weather slides and
// optional special slides for testing the scheduler logic.
func newTestRenderer(weatherNames []string, specials []specialSlideEntry) *Renderer {
	var ws []weatherSlideEntry
	for _, name := range weatherNames {
		ws = append(ws, weatherSlideEntry{name: name, fn: noopSlide})
	}
	return &Renderer{
		weatherSlides: ws,
		specialSlides: specials,
		slideStart:    time.Now(),
	}
}

func TestAdvanceSlide_WeatherCycleRotation(t *testing.T) {
	r := newTestRenderer([]string{"a", "b", "c"}, nil)

	// Starts at index 0.
	if r.currentSlideName() != "a" {
		t.Fatalf("initial slide = %q, want %q", r.currentSlideName(), "a")
	}

	r.advanceSlide(nil)
	if r.currentSlideName() != "b" {
		t.Errorf("after 1st advance = %q, want %q", r.currentSlideName(), "b")
	}

	r.advanceSlide(nil)
	if r.currentSlideName() != "c" {
		t.Errorf("after 2nd advance = %q, want %q", r.currentSlideName(), "c")
	}

	// Wraps around.
	r.advanceSlide(nil)
	if r.currentSlideName() != "a" {
		t.Errorf("after wrap = %q, want %q", r.currentSlideName(), "a")
	}
}

func TestAdvanceSlide_SkipPredicate(t *testing.T) {
	r := newTestRenderer([]string{"a", "b", "c"}, nil)
	// Make slide "b" always skip.
	r.weatherSlides[1].skip = func(*weather.WeatherData) bool { return true }
	data := &weather.WeatherData{}

	r.advanceSlide(data) // a → should skip b → land on c
	if r.currentSlideName() != "c" {
		t.Errorf("after skipping b = %q, want %q", r.currentSlideName(), "c")
	}
}

func TestAdvanceSlide_SkipAllGuard(t *testing.T) {
	r := newTestRenderer([]string{"a", "b"}, nil)
	// All slides skip — should not infinite-loop, stops after one full pass.
	alwaysSkip := func(*weather.WeatherData) bool { return true }
	r.weatherSlides[0].skip = alwaysSkip
	r.weatherSlides[1].skip = alwaysSkip
	data := &weather.WeatherData{}

	r.advanceSlide(data) // should not hang
	// The exact landing slide doesn't matter, just that it terminates.
}

func TestAdvanceSlide_SpecialSlideQueuing(t *testing.T) {
	specials := []specialSlideEntry{
		{name: "ann", fn: noopSlide, getInterval: func() int { return 1 }},
	}
	r := newTestRenderer([]string{"a", "b"}, specials)
	data := &weather.WeatherData{}

	// Cycle through all weather slides to complete one full cycle.
	r.advanceSlide(data) // a → b
	r.advanceSlide(data) // b → wraps, cycle complete, queues "ann"

	if !r.showingSpecial {
		t.Fatal("expected showingSpecial=true after full cycle")
	}
	if r.currentSlideName() != "ann" {
		t.Errorf("special slide = %q, want %q", r.currentSlideName(), "ann")
	}

	// Next advance finishes the special, resumes weather at index 0.
	r.advanceSlide(data)
	if r.showingSpecial {
		t.Error("expected showingSpecial=false after special finishes")
	}
}

func TestAdvanceSlide_SpecialIntervalCounting(t *testing.T) {
	specials := []specialSlideEntry{
		{name: "trivia", fn: noopSlide, getInterval: func() int { return 2 }},
	}
	r := newTestRenderer([]string{"a", "b"}, specials)
	data := &weather.WeatherData{}

	// Complete one weather cycle — interval is 2, so trivia should NOT queue yet.
	r.advanceSlide(data) // a → b
	r.advanceSlide(data) // b → wrap (cycle 1), cyclesSince=1, interval=2, not due
	if r.showingSpecial {
		t.Fatal("trivia should not show after 1 cycle (interval=2)")
	}

	// Complete a second weather cycle — now trivia is due.
	r.advanceSlide(data) // a → b
	r.advanceSlide(data) // b → wrap (cycle 2), cyclesSince=2, interval=2, due!
	if !r.showingSpecial || r.currentSlideName() != "trivia" {
		t.Errorf("expected trivia after 2 cycles, got showingSpecial=%v slide=%q",
			r.showingSpecial, r.currentSlideName())
	}
}

func TestAdvanceSlide_DisabledSpecial(t *testing.T) {
	specials := []specialSlideEntry{
		{name: "disabled", fn: noopSlide, getInterval: func() int { return 0 }},
	}
	r := newTestRenderer([]string{"a"}, specials)
	data := &weather.WeatherData{}

	// Complete several weather cycles — disabled special should never queue.
	for i := 0; i < 10; i++ {
		r.advanceSlide(data)
		if r.showingSpecial {
			t.Fatalf("disabled special was queued on cycle %d", i+1)
		}
	}
}

func TestAdvanceSlide_SpecialSkipPredicate(t *testing.T) {
	specials := []specialSlideEntry{
		{name: "ann", fn: noopSlide, getInterval: func() int { return 1 },
			skip: func() bool { return true }}, // always skip
	}
	r := newTestRenderer([]string{"a"}, specials)
	data := &weather.WeatherData{}

	// Complete a cycle — special is due but its skip predicate says no.
	r.advanceSlide(data) // a → wrap, ann is due but skipped
	if r.showingSpecial {
		t.Error("special with skip=true should not be queued")
	}
}

func TestAdvanceSlide_MultipleSpecials(t *testing.T) {
	specials := []specialSlideEntry{
		{name: "ann", fn: noopSlide, getInterval: func() int { return 1 }},
		{name: "trivia", fn: noopSlide, getInterval: func() int { return 1 }},
	}
	r := newTestRenderer([]string{"a"}, specials)
	data := &weather.WeatherData{}

	// Complete one weather cycle — both specials should queue.
	r.advanceSlide(data) // a → wrap, both due
	if !r.showingSpecial {
		t.Fatal("expected showingSpecial=true")
	}
	first := r.currentSlideName()

	r.advanceSlide(data) // finish first special → show second
	if !r.showingSpecial {
		t.Fatal("expected second special to show")
	}
	second := r.currentSlideName()

	if first == second {
		t.Error("both specials should be different")
	}
	if (first != "ann" && first != "trivia") || (second != "ann" && second != "trivia") {
		t.Errorf("unexpected specials: %q then %q", first, second)
	}

	// Finish the second special — back to weather.
	r.advanceSlide(data)
	if r.showingSpecial {
		t.Error("should be back to weather after both specials shown")
	}
}

func TestShouldSkipCurrent(t *testing.T) {
	r := newTestRenderer([]string{"a", "b"}, nil)
	data := &weather.WeatherData{}

	// No skip predicate → false.
	if r.shouldSkipCurrent(data) {
		t.Error("should not skip when no predicate set")
	}

	// Nil data → false (safety guard).
	if r.shouldSkipCurrent(nil) {
		t.Error("should not skip with nil data")
	}

	// Set skip predicate that returns true.
	r.weatherSlides[0].skip = func(*weather.WeatherData) bool { return true }
	if !r.shouldSkipCurrent(data) {
		t.Error("should skip when predicate returns true")
	}

	// Special slide showing → always false.
	r.showingSpecial = true
	if r.shouldSkipCurrent(data) {
		t.Error("should not skip during special slides")
	}
}
