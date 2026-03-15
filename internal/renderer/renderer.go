package renderer

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"git.sr.ht/~sbinet/gg"
	ann "github.com/zerohalo/weatherrupert/internal/announcements"
	"github.com/zerohalo/weatherrupert/internal/trivia"
	"github.com/zerohalo/weatherrupert/internal/weather"
)

// SlideFunc is a function that draws one weather slide onto dc.
// elapsed is how long the current slide has been showing; total is its full display duration.
// Slides that don't animate can ignore elapsed and total.
// The return value is a requested display duration; 0 means "use the configured default".
type SlideFunc func(dc *gg.Context, data *weather.WeatherData, elapsed, total time.Duration) time.Duration

// weatherSlideEntry pairs a SlideFunc with an optional skip predicate.
// When skip is non-nil and returns true, the slide is not shown.
type weatherSlideEntry struct {
	name string
	fn   SlideFunc
	skip func(*weather.WeatherData) bool
}

// specialSlideEntry pairs a SlideFunc with its own cycle-interval counter.
type specialSlideEntry struct {
	name        string
	fn          SlideFunc
	getInterval func() int  // weather cycles between shows; 0 = disabled
	skip        func() bool // when non-nil and returns true, slide is not queued
	cyclesSince int         // weather cycles since this slide was last shown
}

// Renderer manages the slide show and writes raw RGBA frames to an io.Writer (FFmpeg stdin).
type Renderer struct {
	w, h             int
	frameRate        int
	label            string // short identifier for log messages (e.g. ZIP code)
	getSlideDuration func() time.Duration
	wc               *weather.Client
	out              io.Writer
	fonts            *fontSet // per-renderer font faces (not shared across pipelines)
	weatherSlides    []weatherSlideEntry
	specialSlides    []specialSlideEntry
	hasClients       func() bool // if non-nil, frames are skipped when this returns false

	// slideMu protects slide scheduler state that is read by RenderPreview
	// (HTTP handler goroutine) and written by Run (renderer goroutine).
	slideMu sync.Mutex

	// cachedPreview holds the last successfully rendered preview PNG
	// (when weather data was available). Used as a fallback when the
	// pipeline is suspended and no live render is possible.
	cachedPreview atomic.Pointer[[]byte]

	// slideStart is reset every time the active slide changes.
	// It lets animated slides (radar) distribute frames across the slide duration.
	slideStart time.Time

	// Slide scheduler state.
	weatherIdx      int   // current position in weatherSlides
	specialIdx      int   // index of the currently-showing special slide
	pendingSpecials []int // indices of specials queued to show after the current one
	showingSpecial  bool  // true while a special slide is active
	slideSeq        int   // increments on every slide advance; used as frame cache key

	// Frame cache — avoids re-rendering identical frames.
	cachePix     []byte
	cacheSlide   int   // slideSeq value at last render
	cacheTick    int64 // seconds since slideStart at last render
	cacheFetched int64 // data.FetchedAt.Unix() at last render

	// Loading frame cache — content never changes, so render it once.
	loadingPix []byte

	slowFrames atomic.Int64 // frames where write took >200ms
}

// New creates a Renderer. hasClients may be nil (always render).
// Each special slide (announcements, trivia) carries its own getInterval func so
// that its cycle frequency can be changed live from the admin UI without restart.
// All func() parameters are called on each render tick so that admin changes
// take effect immediately without restarting the pipeline.
func New(w, h, frameRate int, label string,
	getSlideDuration func() time.Duration,
	wc *weather.Client, out io.Writer, hasClients func() bool,
	getAnnouncements func() []ann.Announcement, getAnnDuration func() time.Duration, getAnnInterval func() int,
	getTriviaItems func() []trivia.TriviaItem, getTriviaDuration func() time.Duration, getTriviaInterval func() int, getTriviaRandomize func() bool,
	getPlanetLiveAlways func() bool,
	getRealisticMoon func() bool,
	use24h bool, useMetric bool,
	loc *time.Location,
) *Renderer {
	if loc == nil {
		loc = time.Local
	}
	fonts := newFontSet()
	return &Renderer{
		w:                w,
		h:                h,
		frameRate:        frameRate,
		label:            label,
		getSlideDuration: getSlideDuration,
		wc:               wc,
		out:              out,
		fonts:            fonts,
		weatherSlides: []weatherSlideEntry{
			{name: "alerts", fn: NewSlideAlerts(use24h, loc, fonts), skip: func(d *weather.WeatherData) bool { return len(d.Alerts) == 0 }},
			{name: "current-conditions", fn: NewSlideCurrentConditions(use24h, useMetric, loc, getRealisticMoon, fonts)},
			{name: "hourly-forecast", fn: NewSlideHourlyForecast(use24h, useMetric, loc, getRealisticMoon, fonts)},
			{name: "precipitation", fn: NewSlidePrecipitation(use24h, useMetric, loc, getRealisticMoon, fonts), skip: func() func(*weather.WeatherData) bool {
				noPrecipCount := 0
				return func(d *weather.WeatherData) bool {
					// Check if any hourly period has precipitation probability > 0.
					hasPrecip := false
					for _, p := range d.HourlyPeriods {
						if p.ProbabilityOfPrecipitation.Value != nil && *p.ProbabilityOfPrecipitation.Value > 0 {
							hasPrecip = true
							break
						}
					}
					if hasPrecip {
						noPrecipCount = 0
						return false // always show when there's precipitation
					}
					// No precipitation — show every other cycle.
					noPrecipCount++
					return noPrecipCount%2 == 0
				}
			}()},
			{name: "extended-forecast", fn: NewSlideExtendedForecast(use24h, useMetric, loc, getRealisticMoon, fonts)},
			{name: "moon-tides", fn: NewSlideMoonTides(use24h, useMetric, loc, fonts)},
			{name: "night-sky", fn: NewSlideNightSky(use24h, useMetric, loc, getPlanetLiveAlways, fonts)},
			{name: "solar-weather", fn: NewSlideSolarWeather(use24h, useMetric, loc, fonts), skip: func(d *weather.WeatherData) bool {
				return d.Solar == nil || (d.Solar.SunspotImage == nil && d.Solar.CoronaImage == nil)
			}},
			{name: "satellite", fn: NewSlideSatellite(use24h, loc, fonts), skip: func(d *weather.WeatherData) bool { return len(d.SatelliteFrames) == 0 }},
			{name: "radar", fn: NewSlideRadar(use24h, loc, fonts)},
		},
		specialSlides: []specialSlideEntry{
			{
				name:        "announcements",
				fn:          NewSlideAnnouncements(getAnnouncements, getAnnDuration, use24h, loc, fonts),
				getInterval: getAnnInterval,
				skip: func() bool {
					today := time.Now().Format("01-02")
					for _, a := range getAnnouncements() {
						if a.Date == "" || a.Date == today {
							return false
						}
					}
					return true
				},
			},
			{name: "trivia", fn: NewSlideTrivia(getTriviaItems, getTriviaDuration, getTriviaRandomize, use24h, loc, fonts), getInterval: getTriviaInterval},
		},
		hasClients: hasClients,
		slideStart: time.Now(),
	}
}

// currentSlide returns the SlideFunc that should be rendered right now.
func (r *Renderer) currentSlide() SlideFunc {
	if r.showingSpecial {
		return r.specialSlides[r.specialIdx].fn
	}
	return r.weatherSlides[r.weatherIdx].fn
}

// currentSlideName returns a human-readable name for the active slide.
func (r *Renderer) currentSlideName() string {
	if r.showingSpecial {
		return r.specialSlides[r.specialIdx].name
	}
	return r.weatherSlides[r.weatherIdx].name
}

// shouldSkipCurrent returns true if the current weather slide's skip predicate
// says it should be skipped. Always returns false for special slides.
func (r *Renderer) shouldSkipCurrent(data *weather.WeatherData) bool {
	if r.showingSpecial || data == nil {
		return false
	}
	e := r.weatherSlides[r.weatherIdx]
	return e.skip != nil && e.skip(data)
}

// advanceSlide moves to the next slide according to the scheduler.
// Each special slide has its own cycle interval. After every complete weather
// cycle, any special slide whose counter has reached its interval is queued.
// Queued specials are shown one at a time before the weather cycle resumes.
// data is used to evaluate skip predicates on weather slides.
func (r *Renderer) advanceSlide(data *weather.WeatherData) {
	if r.showingSpecial {
		// Finished a special slide — show the next queued one, or resume weather.
		r.showingSpecial = false
		if len(r.pendingSpecials) > 0 {
			r.specialIdx = r.pendingSpecials[0]
			r.pendingSpecials = r.pendingSpecials[1:]
			r.showingSpecial = true
		}
		return
	}

	r.weatherIdx++
	if r.weatherIdx >= len(r.weatherSlides) {
		r.weatherIdx = 0
		// Tick each special slide's counter and queue those that are due.
		for i := range r.specialSlides {
			r.specialSlides[i].cyclesSince++
			interval := r.specialSlides[i].getInterval()
			if interval > 0 && r.specialSlides[i].cyclesSince >= interval {
				r.specialSlides[i].cyclesSince = 0
				if s := r.specialSlides[i].skip; s != nil && s() {
					continue
				}
				r.pendingSpecials = append(r.pendingSpecials, i)
			}
		}
		if len(r.pendingSpecials) > 0 {
			r.specialIdx = r.pendingSpecials[0]
			r.pendingSpecials = r.pendingSpecials[1:]
			r.showingSpecial = true
		}
	}

	// Skip weather slides whose skip predicate returns true.
	// Guard against skipping all slides (infinite loop) by limiting to one full pass.
	if !r.showingSpecial && data != nil {
		for skipped := 0; skipped < len(r.weatherSlides); skipped++ {
			e := r.weatherSlides[r.weatherIdx]
			if e.skip == nil || !e.skip(data) {
				break
			}
			r.weatherIdx++
			if r.weatherIdx >= len(r.weatherSlides) {
				r.weatherIdx = 0
			}
		}
	}
}

// Run starts the rendering loop. Blocks until ctx is cancelled or a write error occurs.
func (r *Renderer) Run(ctx context.Context) error {
	frameTicker := time.NewTicker(time.Second / time.Duration(r.frameRate))
	defer frameTicker.Stop()

	slideTimer := time.NewTimer(r.getSlideDuration())
	defer slideTimer.Stop()

	// slideJustStarted is true for the first rendered frame of each slide.
	// It lets slides (e.g. Announcements) return a custom display duration that
	// overrides the default slide timer.
	slideJustStarted := true

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-slideTimer.C:
			r.slideMu.Lock()
			r.advanceSlide(r.wc.Current())
			r.slideSeq++
			r.slideStart = time.Now()
			r.slideMu.Unlock()
			slideTimer.Reset(r.getSlideDuration())
			slideJustStarted = true

		case <-frameTicker.C:
			// When nobody is watching, skip writing to FFmpeg stdin entirely.
			// FFmpeg blocks waiting for the next frame (near-zero CPU), and the
			// hub's broadcast loop also blocks waiting for FFmpeg output.
			if r.hasClients != nil && !r.hasClients() {
				continue
			}

			data := r.wc.Current()
			if data == nil {
				if err := r.writeLoadingFrame(); err != nil {
					return err
				}
				continue
			}

			// If the current slide should be skipped (e.g. alerts with no
			// active alerts), advance immediately without rendering a frame.
			if r.shouldSkipCurrent(data) {
				r.slideMu.Lock()
				r.advanceSlide(data)
				r.slideSeq++
				r.slideStart = time.Now()
				r.slideMu.Unlock()
				slideTimer.Reset(r.getSlideDuration())
				slideJustStarted = true
				continue
			}

			// Re-render only when something visible has changed.
			// tick = whole seconds elapsed since the slide started; changes once
			// per second so animated slides (radar) advance frames on schedule,
			// while static slides still only re-render when the data changes.
			elapsed := time.Since(r.slideStart)
			tick := int64(elapsed.Seconds())
			fetched := data.FetchedAt.Unix()
			if r.cachePix != nil &&
				r.slideSeq == r.cacheSlide &&
				tick == r.cacheTick &&
				fetched == r.cacheFetched {
				// Content unchanged — reuse the last rendered frame.
				if err := r.writeFrame(r.cachePix); err != nil {
					return err
				}
				continue
			}

			slideName := r.currentSlideName()
			pix, slideDur, err := renderSlide(r.w, r.h, r.currentSlide(), data, elapsed, r.getSlideDuration(), slideName)
			if err != nil {
				log.Printf("renderer: render slide %q error: %v", slideName, err)
				continue
			}
			r.cachePix = pix
			r.cacheSlide = r.slideSeq
			r.cacheTick = tick
			r.cacheFetched = fetched

			// On the first rendered frame of a new slide, honour any custom
			// duration the slide returned (e.g. per-announcement timing).
			if slideJustStarted {
				if slideDur > 0 && slideDur != r.getSlideDuration() {
					slideTimer.Reset(slideDur)
				}
				// Snapshot a PNG for the preview cache on each slide change.
				r.updateCachedPreview(pix)
			}
			slideJustStarted = false

			if err := r.writeFrame(pix); err != nil {
				return err
			}
		}
	}
}

// renderSlide creates a new drawing context, calls the slide function, and returns
// raw RGBA bytes plus any custom display duration the slide requested (0 = use default).
// Panics inside a slide (e.g. x/image rasterizer overflow) are recovered so that
// one bad frame does not crash the entire rendering pipeline.
func renderSlide(w, h int, slide SlideFunc, data *weather.WeatherData, elapsed, total time.Duration, name string) (pix []byte, dur time.Duration, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("slide %q panic: %v\n%s", name, r, debug.Stack())
		}
	}()
	dc := gg.NewContext(w, h)
	dur = slide(dc, data, elapsed, total)
	pix, err = extractPixels(dc)
	return
}

// writeLoadingFrame renders and caches a loading screen shown before weather data is ready.
// The content never changes, so it is rendered once and the pixel buffer reused.
func (r *Renderer) writeLoadingFrame() error {
	if r.loadingPix == nil {
		dc := gg.NewContext(r.w, r.h)
		DrawGradientBackground(dc)

		// Header rule
		dc.SetRGBA(1, 1, 1, 0.25)
		dc.DrawRectangle(0, headerH, float64(r.w), 2)
		dc.Fill()

		// Title
		dc.SetFontFace(r.fonts.title)
		drawShadowText(dc, "LOCAL WEATHER", 60, 54, titleR, titleG, titleB)

		// Loading message
		dc.SetFontFace(r.fonts.medium)
		drawShadowTextAnchored(dc, "LOADING WEATHER DATA...", float64(r.w)/2, float64(r.h)/2, 0.5, 0.5, subR, subG, subB)

		pix, err := extractPixels(dc)
		if err != nil {
			return err
		}
		r.loadingPix = pix
		log.Printf("renderer %s: loading frame rendered (%d bytes)", r.label, len(pix))
	}

	_, err := r.out.Write(r.loadingPix)
	return err
}

// writeFrame writes a raw RGBA frame to FFmpeg stdin and logs a warning
// when the write takes longer than expected.  A slow write means FFmpeg's
// input pipe buffer is full because it can't encode frames fast enough —
// the most direct indicator of CPU contention.
func (r *Renderer) writeFrame(pix []byte) error {
	t0 := time.Now()
	_, err := r.out.Write(pix)
	if d := time.Since(t0); d > 200*time.Millisecond {
		r.slowFrames.Add(1)
		log.Printf("renderer [%s]: slow frame write: %v (%d bytes) — FFmpeg may be CPU-starved",
			r.label, d, len(pix))
	}
	if err != nil {
		return fmt.Errorf("renderer: write frame: %w", err)
	}
	return nil
}

// SlowFrames returns the number of frame writes that took longer than 200ms.
func (r *Renderer) SlowFrames() int64 { return r.slowFrames.Load() }

// extractPixels returns the raw RGBA pixel bytes from a gg.Context.
// The underlying image type is *image.RGBA, whose Pix slice is in R,G,B,A order —
// matching FFmpeg's "rgba" pixel format.
func extractPixels(dc *gg.Context) ([]byte, error) {
	rgba, ok := dc.Image().(*image.RGBA)
	if !ok {
		return nil, fmt.Errorf("renderer: gg.Context.Image() returned unexpected type")
	}
	// Return a copy so the context can be GC'd independently.
	pix := make([]byte, len(rgba.Pix))
	copy(pix, rgba.Pix)
	return pix, nil
}

// RenderPreview renders the current slide as a PNG image and returns the bytes.
// When weather data is available, the result is cached so it can be served
// later via CachedPreview even after the pipeline is suspended.
func (r *Renderer) RenderPreview() ([]byte, error) {
	data := r.wc.Current()
	if data == nil {
		// Return cached preview if available, otherwise render loading screen.
		if cached := r.CachedPreview(); cached != nil {
			return cached, nil
		}
		dc := gg.NewContext(r.w, r.h)
		DrawGradientBackground(dc)
		dc.SetRGBA(1, 1, 1, 0.25)
		dc.DrawRectangle(0, headerH, float64(r.w), 2)
		dc.Fill()
		dc.SetFontFace(r.fonts.title)
		drawShadowText(dc, "LOCAL WEATHER", 60, 54, titleR, titleG, titleB)
		dc.SetFontFace(r.fonts.medium)
		drawShadowTextAnchored(dc, "LOADING WEATHER DATA...", float64(r.w)/2, float64(r.h)/2, 0.5, 0.5, subR, subG, subB)
		return encodePNG(dc)
	}

	r.slideMu.Lock()
	slideName := r.currentSlideName()
	slide := r.currentSlide()
	slideStart := r.slideStart
	r.slideMu.Unlock()

	dc := gg.NewContext(r.w, r.h)

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("renderer: preview slide %q panic: %v\n%s", slideName, rec, debug.Stack())
			}
		}()
		elapsed := time.Since(slideStart)
		slide(dc, data, elapsed, r.getSlideDuration())
	}()

	png, err := encodePNG(dc)
	if err == nil {
		r.cachedPreview.Store(&png)
	}
	return png, err
}

// CachedPreview returns the last successfully rendered preview PNG, or nil
// if no preview has been rendered yet.
func (r *Renderer) CachedPreview() []byte {
	if p := r.cachedPreview.Load(); p != nil {
		return *p
	}
	return nil
}

// updateCachedPreview encodes the raw RGBA pixel buffer as a PNG and stores
// it in cachedPreview. Called from the render loop on slide transitions.
func (r *Renderer) updateCachedPreview(pix []byte) {
	img := &image.RGBA{
		Pix:    pix,
		Stride: r.w * 4,
		Rect:   image.Rect(0, 0, r.w, r.h),
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return
	}
	b := buf.Bytes()
	r.cachedPreview.Store(&b)
}

// RenderPlaceholderPreview generates a standalone preview PNG without needing
// a running pipeline. Used when no pipeline exists and no cached preview is
// available (e.g. guide art fetches that should not spin up a pipeline).
func RenderPlaceholderPreview(w, h int) ([]byte, error) {
	fonts := newFontSet()
	dc := gg.NewContext(w, h)
	DrawGradientBackground(dc)
	dc.SetRGBA(1, 1, 1, 0.25)
	dc.DrawRectangle(0, headerH, float64(w), 2)
	dc.Fill()

	// Header: title + logo sun
	dc.SetFontFace(fonts.title)
	drawShadowText(dc, "LOCAL WEATHER", 60, 54, titleR, titleG, titleB)
	DrawLogoSun(dc, float64(w)/2-100, headerH/2, 30)
	dc.SetFontFace(fonts.small)
	drawShadowText(dc, "WEATHER RUPERT", float64(w)/2-62, headerH/2+7, textR, textG, textB)

	return encodePNG(dc)
}

// RenderFavicon generates a 32x32 PNG favicon using the sun logo.
func RenderFavicon() ([]byte, error) {
	dc := gg.NewContext(32, 32)
	DrawLogoSun(dc, 16, 16, 32)
	return encodePNG(dc)
}

func encodePNG(dc *gg.Context) ([]byte, error) {
	img, ok := dc.Image().(*image.RGBA)
	if !ok {
		return nil, fmt.Errorf("renderer: unexpected image type")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("renderer: png encode: %w", err)
	}
	return buf.Bytes(), nil
}
