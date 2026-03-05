package renderer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"git.sr.ht/~sbinet/gg"
	ann "github.com/zerohalo/weatherrupert/internal/announcements"
	"github.com/zerohalo/weatherrupert/internal/trivia"
	"github.com/zerohalo/weatherrupert/internal/weather"
	xdraw "golang.org/x/image/draw"
)

// WeatherStar 4000+ authentic color palette (R, G, B as 0–1 floats).
const (
	// bgR/G/B is the midpoint of the screen gradient (#102080→#001040 at t=0.5).
	// Used only by icon code that needs to "carve" shapes against the background.
	bgR, bgG, bgB = 0.031, 0.094, 0.376

	titleR, titleG, titleB    = 1.0, 1.0, 0.0     // #FFFF00 yellow — screen titles, day names
	hlR, hlG, hlB             = 1.0, 1.0, 0.0     // yellow highlight (same as title)
	textR, textG, textB       = 1.0, 1.0, 1.0     // white — body text, date/time
	lowR, lowG, lowB          = 0.502, 0.502, 1.0 // #8080FF periwinkle — low temp, wind chill
	heatR, heatG, heatB       = 0.878, 0.0, 0.0   // red — heat index
	divR, divG, divB          = 0.0, 0.604, 0.804 // #009ACD cyan-blue — rain streaks in icons
	colHdrR, colHdrG, colHdrB = 0.125, 0.0, 0.341 // rgb(32,0,87) deep indigo — column header bg
	subR, subG, subB          = 0.75, 0.75, 0.75  // gray — secondary / label text
)

// headerH is the height (px) of the top header band shared by all screens.
const headerH = 90.0

// circledLetterCache holds pre-rendered circled-letter icons keyed by
// "letter:size" so they are computed once and reused.
var (
	circledLetterMu    sync.Mutex
	circledLetterCache = map[string]image.Image{}
)

// circledLetter returns a square image.Image of a yellow circle with a black
// bold letter centered inside. The diameter equals size pixels.
func circledLetter(letter string, size int) image.Image {
	key := fmt.Sprintf("%s:%d", letter, size)
	circledLetterMu.Lock()
	defer circledLetterMu.Unlock()
	if img, ok := circledLetterCache[key]; ok {
		return img
	}
	dc := gg.NewContext(size, size)
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size)/2 - 1
	dc.DrawCircle(cx, cy, r)
	dc.SetRGB(titleR, titleG, titleB)
	dc.Fill()
	// Use a font size ~90% of the circle diameter for a clear, readable letter.
	fontSize := float64(size) * 0.95
	face, err := gg.LoadFontFaceFromBytes(inconsolataBoldTTF, fontSize)
	if err == nil {
		dc.SetFontFace(face)
	}
	dc.SetRGB(0, 0, 0)
	// Center the letter using font metrics. DrawString positions at the
	// baseline, so we need the ascent (top of caps to baseline) to find the
	// true visual center of a capital letter.
	w, _ := dc.MeasureString(letter)
	met := face.Metrics()
	ascent := float64(met.Ascent) / 64 // fixed-point 26.6 → float
	dc.DrawString(letter, cx-w/2, cy+ascent/2-ascent*0.08)
	img := dc.Image()
	circledLetterCache[key] = img
	return img
}

// gradCache holds a pre-rendered gradient image to avoid recomputing it every frame.
var (
	gradMu       sync.Mutex
	gradImg      *image.RGBA
	gradW, gradH int
)

// getGradientBG returns a cached *image.RGBA with the WS4000+ gradient.
// The gradient is computed once per canvas size and reused on every frame.
func getGradientBG(w, h int) *image.RGBA {
	gradMu.Lock()
	defer gradMu.Unlock()
	if gradImg != nil && gradW == w && gradH == h {
		return gradImg
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	hf := float64(h - 1)
	for y := 0; y < h; y++ {
		t := float64(y) / hf
		// #102080 → #001040
		rVal := uint8(math.Round(float64(0x10) * (1.0 - t)))
		gVal := uint8(math.Round(float64(0x20)*(1.0-t) + float64(0x10)*t))
		bVal := uint8(math.Round(float64(0x80)*(1.0-t) + float64(0x40)*t))
		off := img.PixOffset(0, y)
		for x := 0; x < w; x++ {
			img.Pix[off+x*4+0] = rVal
			img.Pix[off+x*4+1] = gVal
			img.Pix[off+x*4+2] = bVal
			img.Pix[off+x*4+3] = 0xff
		}
	}
	gradImg = img
	gradW, gradH = w, h
	return img
}

// DrawGradientBackground fills dc with the WS4000+ vertical gradient (#102080 → #001040).
// The gradient is pre-rendered once and copied directly into the context's pixel buffer.
// Exported so renderer.go can use it for the loading screen.
func DrawGradientBackground(dc *gg.Context) {
	bg := getGradientBG(dc.Width(), dc.Height())
	dst := dc.Image().(*image.RGBA)
	copy(dst.Pix, bg.Pix)
}

// drawBackground fills the gradient and renders the header common to all slides:
// yellow screen title (left), white location (left below title),
// and current date/time (right). use24h selects 24-hour vs 12-hour clock format.
func drawBackground(dc *gg.Context, title, location string, use24h bool, loc *time.Location, fonts *fontSet) {
	if loc == nil {
		loc = time.Local
	}
	w := float64(dc.Width())
	DrawGradientBackground(dc)

	// Subtle horizontal rule below header
	dc.SetRGBA(1, 1, 1, 0.25)
	dc.DrawRectangle(0, headerH, w, 2)
	dc.Fill()

	// Screen title — yellow, left side
	dc.SetFontFace(fonts.title)
	drawShadowText(dc, strings.ToUpper(title), 60, 56, titleR, titleG, titleB)

	// Location — white, smaller, below title
	if location != "" {
		dc.SetFontFace(fonts.small)
		drawShadowText(dc, truncate(strings.ToUpper(location), 42), 60, 80, textR, textG, textB)
	}

	// Logo: small sun icon + "WEATHER RUPERT" branding, centered in header
	DrawLogoSun(dc, w/2-100, headerH/2, 30)
	dc.SetFontFace(fonts.small)
	drawShadowTextAnchored(dc, "WEATHER RUPERT", w/2+10, headerH/2, 0.5, 0.5, titleR, titleG, titleB)

	// Date + time — right-aligned, vertically centred in the header band
	now := time.Now().In(loc)
	timeFmt := "3:04 PM"
	if use24h {
		timeFmt = "15:04"
	}
	dc.SetFontFace(fonts.small)
	drawShadowTextAnchored(dc, now.Format("Mon Jan 2"), w-50, 40, 1.0, 0.5, textR, textG, textB)
	drawShadowTextAnchored(dc, now.Format(timeFmt+" MST"), w-50, 64, 1.0, 0.5, textR, textG, textB)
}

// drawShadowText draws s at (x, y) baseline-left with a dark drop-shadow.
// The caller must set the desired font face on dc before calling.
func drawShadowText(dc *gg.Context, s string, x, y, r, g, b float64) {
	dc.SetRGBA(0, 0, 0, 0.85)
	dc.DrawString(s, x+2, y+2)
	dc.SetRGB(r, g, b)
	dc.DrawString(s, x, y)
}

// drawShadowTextAnchored draws s anchored at (ax, ay) with a dark drop-shadow.
func drawShadowTextAnchored(dc *gg.Context, s string, x, y, ax, ay, r, g, b float64) {
	dc.SetRGBA(0, 0, 0, 0.85)
	dc.DrawStringAnchored(s, x+2, y+2, ax, ay)
	dc.SetRGB(r, g, b)
	dc.DrawStringAnchored(s, x, y, ax, ay)
}

// currentIsDaytime returns true if the observation time is during daylight.
func currentIsDaytime(data *weather.WeatherData, loc *time.Location) bool {
	if loc == nil {
		loc = time.Local
	}
	if len(data.HourlyPeriods) > 0 {
		return data.HourlyPeriods[0].IsDaytime
	}
	if !data.Current.UpdatedAt.IsZero() {
		h := data.Current.UpdatedAt.In(loc).Hour()
		return h >= 6 && h < 20
	}
	return true
}

// ── Unit conversion helpers (display-time only; stored data remains Imperial) ──

func fToC(f float64) float64         { return (f - 32) * 5 / 9 }
func mphToKmh(mph float64) float64   { return mph * 1.60934 }
func milesToKm(mi float64) float64   { return mi * 1.60934 }
func inHgToHPa(inHg float64) float64 { return inHg * 33.8639 }

// fmtOr formats a nullable float with the given format string, or returns
// the fallback string (typically "—") when the value is nil.
func fmtOr(v *float64, format, fallback string) string {
	if v == nil {
		return fallback
	}
	return fmt.Sprintf(format, *v)
}

// fmtConvOr formats a nullable float through a conversion function, or
// returns the fallback when nil.
func fmtConvOr(v *float64, conv func(float64) float64, format, fallback string) string {
	if v == nil {
		return fallback
	}
	return fmt.Sprintf(format, conv(*v))
}

// NewSlideCurrentConditions returns a SlideFunc that renders current temperature,
// conditions, and atmospheric data. use24h controls the clock format in the header.
func NewSlideCurrentConditions(use24h, useMetric bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	return func(dc *gg.Context, data *weather.WeatherData, _, _ time.Duration) time.Duration {
		return slideCurrentConditions(dc, data, use24h, useMetric, loc, fonts)
	}
}

func slideCurrentConditions(dc *gg.Context, data *weather.WeatherData, use24h, useMetric bool, loc *time.Location, fonts *fontSet) time.Duration {
	drawBackground(dc, "LOCAL CONDITIONS", data.Location, use24h, loc, fonts)

	w := float64(dc.Width())
	h := float64(dc.Height())
	cur := data.Current
	contentY := headerH + 8
	midX := w / 2

	// ── Left column: weather icon with condition label below ──
	iconSize := 380.0
	iconCX := midX / 2
	iconCY := (contentY+h)/2 - 30
	icon := conditionIcon(cur.Description, currentIsDaytime(data, loc))
	drawIcon(dc, icon, iconCX, iconCY, iconSize)

	// Condition description — centred below the icon
	dc.SetFontFace(fonts.mediumBold)
	drawShadowTextAnchored(dc, strings.ToUpper(truncate(cur.Description, 20)), iconCX, iconCY+iconSize/2+30, 0.5, 0.5, subR, subG, subB)

	// ── Vertical divider ──
	dc.SetRGBA(1, 1, 1, 0.20)
	dc.DrawRectangle(midX, contentY, 2, h-contentY-20)
	dc.Fill()

	// ── Right column: temperature and stats ──
	textX := midX + 20

	// Temperature — hero, yellow
	dc.SetFontFace(fonts.hero)
	if useMetric {
		drawShadowText(dc, fmtConvOr(cur.TempF, fToC, "%.0f°C", "—"), textX, contentY+78, hlR, hlG, hlB)
	} else {
		drawShadowText(dc, fmtOr(cur.TempF, "%.0f°F", "—"), textX, contentY+78, hlR, hlG, hlB)
	}

	// Wind
	dc.SetFontFace(fonts.mediumXL)
	if useMetric {
		windStr := "WIND: " + cur.WindDir + "  " + fmtConvOr(cur.WindSpeedMph, mphToKmh, "%.0f KM/H", "— KM/H")
		drawShadowText(dc, windStr, textX, contentY+137, textR, textG, textB)
	} else {
		windStr := "WIND: " + cur.WindDir + "  " + fmtOr(cur.WindSpeedMph, "%.0f MPH", "— MPH")
		drawShadowText(dc, windStr, textX, contentY+137, textR, textG, textB)
	}

	// Track the next available Y for optional lines (gusts, heat index, wind chill).
	nextY := contentY + 195.0
	if cur.WindGustMph != nil {
		if useMetric {
			drawShadowText(dc, fmt.Sprintf("GUSTS TO %.0f KM/H", mphToKmh(*cur.WindGustMph)), textX, nextY, textR, textG, textB)
		} else {
			drawShadowText(dc, fmt.Sprintf("GUSTS TO %.0f MPH", *cur.WindGustMph), textX, nextY, textR, textG, textB)
		}
		nextY += 52
	}
	if cur.HeatIndexF != nil && cur.TempF != nil && *cur.HeatIndexF > *cur.TempF+2 {
		if useMetric {
			drawShadowText(dc, fmt.Sprintf("HEAT INDEX: %.0f°C", fToC(*cur.HeatIndexF)), textX, nextY, heatR, heatG, heatB)
		} else {
			drawShadowText(dc, fmt.Sprintf("HEAT INDEX: %.0f°F", *cur.HeatIndexF), textX, nextY, heatR, heatG, heatB)
		}
		nextY += 52
	} else if cur.WindChillF != nil && cur.TempF != nil && *cur.WindChillF < *cur.TempF-2 {
		if useMetric {
			drawShadowText(dc, fmt.Sprintf("WIND CHILL: %.0f°C", fToC(*cur.WindChillF)), textX, nextY, lowR, lowG, lowB)
		} else {
			drawShadowText(dc, fmt.Sprintf("WIND CHILL: %.0f°F", *cur.WindChillF), textX, nextY, lowR, lowG, lowB)
		}
		nextY += 52
	}

	// 2×2 stats grid flows directly below the text block.
	type cell struct{ label, value string }
	var cells []cell
	if useMetric {
		cells = []cell{
			{"HUMIDITY", fmtOr(cur.Humidity, "%.0f%%", "—")},
			{"DEWPOINT", fmtConvOr(cur.DewpointF, fToC, "%.0f°C", "—")},
			{"PRESSURE", fmtConvOr(cur.PressureInHg, inHgToHPa, "%.0f HPA", "—")},
			{"VISIBILITY", fmtConvOr(cur.VisibilityMi, milesToKm, "%.1f KM", "—")},
		}
	} else {
		cells = []cell{
			{"HUMIDITY", fmtOr(cur.Humidity, "%.0f%%", "—")},
			{"DEWPOINT", fmtOr(cur.DewpointF, "%.0f°F", "—")},
			{"PRESSURE", fmtOr(cur.PressureInHg, "%.2f IN", "—")},
			{"VISIBILITY", fmtOr(cur.VisibilityMi, "%.1f MI", "—")},
		}
	}

	cellW := (w - midX) / 2
	const cellRowH = 125.0
	gridStartY := nextY + 25.0

	for i, c := range cells {
		col := float64(i % 2)
		row := float64(i / 2)
		cx := midX + col*cellW + cellW/2
		labelY := gridStartY + row*cellRowH
		valueY := labelY + 48

		dc.SetFontFace(fonts.mediumBold)
		drawShadowTextAnchored(dc, c.label+":", cx, labelY, 0.5, 0.5, subR, subG, subB)
		dc.SetFontFace(fonts.xl)
		drawShadowTextAnchored(dc, c.value, cx, valueY, 0.5, 0.5, textR, textG, textB)
	}
	return 0
}

// NewSlideHourlyForecast returns a SlideFunc that renders the next 12 hours as a
// temperature line graph. use24h controls the clock format for axis labels and the header.
func NewSlideHourlyForecast(use24h, useMetric bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	return func(dc *gg.Context, data *weather.WeatherData, _, _ time.Duration) time.Duration {
		return slideHourlyForecast(dc, data, use24h, useMetric, loc, fonts)
	}
}

func slideHourlyForecast(dc *gg.Context, data *weather.WeatherData, use24h, useMetric bool, loc *time.Location, fonts *fontSet) time.Duration {
	drawBackground(dc, "HOURLY FORECAST", data.Location, use24h, loc, fonts)

	w := float64(dc.Width())
	h := float64(dc.Height())

	if len(data.HourlyPeriods) == 0 {
		dc.SetFontFace(fonts.medium)
		drawShadowTextAnchored(dc, "NO DATA AVAILABLE", w/2, h/2, 0.5, 0.5, subR, subG, subB)
		return 0
	}

	periods := data.HourlyPeriods
	if len(periods) > 12 {
		periods = periods[:12]
	}
	n := len(periods)

	// Convert temperatures to display unit.
	temps := make([]float64, n)
	for i, p := range periods {
		t := float64(p.Temperature)
		if useMetric {
			if p.TemperatureUnit == "F" {
				t = fToC(t)
			}
		} else {
			if p.TemperatureUnit == "C" {
				t = t*9/5 + 32
			}
		}
		temps[i] = t
	}

	// Compute temperature range with padding so icons never touch the edges.
	minT, maxT := temps[0], temps[0]
	for _, t := range temps {
		if t < minT {
			minT = t
		}
		if t > maxT {
			maxT = t
		}
	}
	minT -= 10
	maxT += 10

	// Plot area bounds — shifted right so the chart is visually centred
	// when the Y-axis labels (~55 px wide) are taken into account.
	const (
		plotLeft   = 150.0
		plotRight  = 1230.0
		plotBottom = 638.0
		iconSize   = 79.0
		// xPad insets data-point positions from the plot edges so that icons
		// (±iconSize/2 = ±39.5 px wide) never overlap the Y-axis labels which
		// are right-anchored at plotLeft-6.  A 50 px inset leaves ~16 px of
		// clearance between the first/last icon edge and the nearest label.
		xPad = 50.0
	)
	plotTop := headerH + 120.0 // headroom above topmost icon + temp label
	plotW := plotRight - plotLeft
	plotH := plotBottom - plotTop

	// Map a temperature value to a Y pixel coordinate.
	tempToY := func(t float64) float64 {
		return plotBottom - (t-minT)/(maxT-minT)*plotH
	}
	// Map a period index to an X pixel coordinate.
	// Data points are inset by xPad on each side so icons don't overlap
	// the Y-axis labels; the gridlines and area-fill base still span the
	// full plotLeft–plotRight width.
	idxToX := func(i int) float64 {
		if n <= 1 {
			return plotLeft + plotW/2
		}
		return plotLeft + xPad + float64(i)*(plotW-2*xPad)/float64(n-1)
	}

	// Pre-compute all data-point positions.
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i := range periods {
		xs[i] = idxToX(i)
		ys[i] = tempToY(temps[i])
	}

	// ── Y-axis grid lines ──
	const gridLines = 4
	for g := 0; g <= gridLines; g++ {
		gTemp := minT + float64(g)*(maxT-minT)/float64(gridLines)
		gY := tempToY(gTemp)
		dc.SetRGBA(1, 1, 1, 0.07)
		dc.SetLineWidth(1)
		dc.DrawLine(plotLeft, gY, plotRight, gY)
		dc.Stroke()
		dc.SetFontFace(fonts.small)
		drawShadowTextAnchored(dc, fmt.Sprintf("%.0f°", gTemp), plotLeft-6, gY, 1.0, 0.5, subR, subG, subB)
	}

	// ── Area fill under the curve ──
	// The base extends to the full plotLeft/plotRight so the fill doesn't
	// appear clipped even though data points are inset by xPad.
	if n > 1 {
		dc.SetRGBA(hlR, hlG, 0, 0.07)
		dc.MoveTo(plotLeft, plotBottom)
		dc.LineTo(xs[0], ys[0])
		for i := 1; i < n; i++ {
			dc.LineTo(xs[i], ys[i])
		}
		dc.LineTo(xs[n-1], plotBottom)
		dc.LineTo(plotRight, plotBottom)
		dc.ClosePath()
		dc.Fill()
	}

	// ── Icons, temperature labels, and time labels at each data point ──
	unit := "°"
	for i, p := range periods {
		x, y := xs[i], ys[i]

		// Temperature label above the icon.
		dc.SetFontFace(fonts.small)
		drawShadowTextAnchored(dc, fmt.Sprintf("%.0f%s", temps[i], unit),
			x, y-iconSize/2-32, 0.5, 1.0, hlR, hlG, hlB)

		// Weather icon centered on the data point (draws over the line).
		icon := conditionIcon(p.ShortForecast, p.IsDaytime)
		drawIcon(dc, icon, x, y, iconSize)

		// Time label below the X axis.
		drawShadowTextAnchored(dc, hourLabel(p.StartTime, use24h, loc, i),
			x, plotBottom+26, 0.5, 0.5, textR, textG, textB)
	}
	return 0
}

// hourLabel formats an RFC3339 start time for the hourly chart X-axis.
// In 24h mode it returns the bare hour ("0"–"23"); in 12h mode it returns
// "12AM", "3PM", etc. fallback is used when the time string can't be parsed.
func hourLabel(startTime string, use24h bool, loc *time.Location, fallback int) string {
	if loc == nil {
		loc = time.Local
	}
	if t, err := time.Parse(time.RFC3339, startTime); err == nil {
		h := t.In(loc).Hour()
		if use24h {
			return fmt.Sprintf("%d", h)
		}
		suffix := "AM"
		if h >= 12 {
			suffix = "PM"
		}
		if h == 0 {
			h = 12
		} else if h > 12 {
			h -= 12
		}
		return fmt.Sprintf("%d%s", h, suffix)
	}
	return fmt.Sprintf("%d", fallback+1)
}

// dayCard pairs a daytime and optional nighttime forecast period into a single card.
type dayCard struct {
	name     string
	forecast string
	daytime  bool
	hasHigh  bool
	highTemp int
	highUnit string
	hasLow   bool
	lowTemp  int
	lowUnit  string
}

// buildDayCards pairs consecutive day/night periods into unified day cards.
func buildDayCards(periods []weather.ForecastPeriod) []dayCard {
	var cards []dayCard
	for i := 0; i < len(periods); {
		p := periods[i]
		if p.IsDaytime {
			card := dayCard{
				name:     p.Name,
				forecast: p.ShortForecast,
				daytime:  true,
				hasHigh:  true,
				highTemp: p.Temperature,
				highUnit: p.TemperatureUnit,
			}
			// Peek at the next period — if it's the paired night, consume it.
			if i+1 < len(periods) && !periods[i+1].IsDaytime {
				i++
				n := periods[i]
				card.hasLow = true
				card.lowTemp = n.Temperature
				card.lowUnit = n.TemperatureUnit
			}
			cards = append(cards, card)
		} else {
			// Orphan night period (e.g. "Tonight" at the start of the forecast)
			name := strings.TrimSuffix(p.Name, " Night")
			cards = append(cards, dayCard{
				name:     name,
				forecast: p.ShortForecast,
				daytime:  false,
				hasLow:   true,
				lowTemp:  p.Temperature,
				lowUnit:  p.TemperatureUnit,
			})
		}
		i++
	}
	return cards
}

// NewSlideExtendedForecast returns a SlideFunc that renders a 3×2 grid of day cards.
// use24h controls the clock format in the header.
func NewSlideExtendedForecast(use24h, useMetric bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	return func(dc *gg.Context, data *weather.WeatherData, _, _ time.Duration) time.Duration {
		return slideExtendedForecast(dc, data, use24h, useMetric, loc, fonts)
	}
}

func slideExtendedForecast(dc *gg.Context, data *weather.WeatherData, use24h, useMetric bool, loc *time.Location, fonts *fontSet) time.Duration {
	drawBackground(dc, "EXTENDED FORECAST", data.Location, use24h, loc, fonts)

	if len(data.DailyPeriods) == 0 {
		dc.SetFontFace(fonts.medium)
		drawShadowTextAnchored(dc, "NO DATA AVAILABLE", float64(dc.Width())/2, float64(dc.Height())/2, 0.5, 0.5, subR, subG, subB)
		return 0
	}

	cards := buildDayCards(data.DailyPeriods)
	if len(cards) > 6 {
		cards = cards[:6]
	}

	const (
		numCols = 3
		numRows = 2
		padX    = 24.0
		padY    = 10.0
		gapX    = 16.0
		gapY    = 12.0
	)

	w := float64(dc.Width())
	contentTop := headerH + 6
	contentH := float64(dc.Height()) - contentTop
	cardW := (w - 2*padX - float64(numCols-1)*gapX) / float64(numCols)
	cardH := (contentH - 2*padY - float64(numRows-1)*gapY) / float64(numRows)
	iconSize := math.Min(cardH*0.38, 100.0)

	for i, c := range cards {
		col := i % numCols
		row := i / numCols
		cardX := padX + float64(col)*(cardW+gapX)
		cardY := contentTop + padY + float64(row)*(cardH+gapY)
		cx := cardX + cardW/2 // horizontal center of card

		// Subtle card background
		dc.SetRGBA(0, 0, 0, 0.18)
		dc.DrawRoundedRectangle(cardX, cardY, cardW, cardH, 4)
		dc.Fill()

		// Card border
		dc.SetRGBA(1, 1, 1, 0.18)
		dc.SetLineWidth(1)
		dc.DrawRoundedRectangle(cardX, cardY, cardW, cardH, 4)
		dc.Stroke()

		// Day name — yellow, centered (60pt bold; keep clear of card top)
		dc.SetFontFace(fonts.cardTitle)
		drawShadowTextAnchored(dc, strings.ToUpper(c.name), cx, cardY+52, 0.5, 0.5, titleR, titleG, titleB)

		// Condition text — white, one line, centered
		dc.SetFontFace(fonts.cardBody)
		drawShadowTextAnchored(dc, truncate(strings.ToUpper(c.forecast), 22), cx, cardY+98, 0.5, 0.5, textR, textG, textB)

		// Weather icon — centered in the card's middle band, shifted down for larger header
		icon := conditionIcon(c.forecast, c.daytime)
		drawIcon(dc, icon, cx, cardY+cardH*0.57, iconSize)

		// Temperature block — high and/or low
		dc.SetFontFace(fonts.medium)
		tempY := cardY + cardH - 28
		if c.hasHigh && c.hasLow {
			// Both: high on left, low on right
			highStr := tempStr(c.highTemp, c.highUnit, useMetric)
			lowStr := tempStr(c.lowTemp, c.lowUnit, useMetric)
			// Labels
			dc.SetFontFace(fonts.small)
			drawShadowTextAnchored(dc, "HIGH", cardX+cardW*0.27, tempY-26, 0.5, 0.5, subR, subG, subB)
			drawShadowTextAnchored(dc, "LOW", cardX+cardW*0.73, tempY-26, 0.5, 0.5, subR, subG, subB)
			// Values
			dc.SetFontFace(fonts.medium)
			drawShadowTextAnchored(dc, highStr, cardX+cardW*0.27, tempY, 0.5, 0.5, hlR, hlG, hlB)
			drawShadowTextAnchored(dc, lowStr, cardX+cardW*0.73, tempY, 0.5, 0.5, lowR, lowG, lowB)
		} else if c.hasHigh {
			dc.SetFontFace(fonts.small)
			drawShadowTextAnchored(dc, "HIGH", cx, tempY-26, 0.5, 0.5, subR, subG, subB)
			dc.SetFontFace(fonts.medium)
			drawShadowTextAnchored(dc, tempStr(c.highTemp, c.highUnit, useMetric), cx, tempY, 0.5, 0.5, hlR, hlG, hlB)
		} else if c.hasLow {
			dc.SetFontFace(fonts.small)
			drawShadowTextAnchored(dc, "LOW", cx, tempY-26, 0.5, 0.5, subR, subG, subB)
			dc.SetFontFace(fonts.medium)
			drawShadowTextAnchored(dc, tempStr(c.lowTemp, c.lowUnit, useMetric), cx, tempY, 0.5, 0.5, lowR, lowG, lowB)
		}
	}
	return 0
}

// tempStr formats a temperature value, converting to the target unit system.
func tempStr(temp int, unit string, useMetric bool) string {
	t := float64(temp)
	if useMetric {
		if unit == "F" {
			t = fToC(t)
		}
		return fmt.Sprintf("%.0f°C", t)
	}
	if unit == "C" {
		t = t*9/5 + 32
	}
	return fmt.Sprintf("%.0f°F", t)
}

// radarFrameCache holds pre-decoded and pre-scaled radar images so that
// png.Decode + xdraw.BiLinear.Scale only run once per weather data refresh
// rather than once per animation frame render.
type radarFrameCache struct {
	fetched int64         // data.FetchedAt.Unix() when cache was built
	scaled  []*image.RGBA // one entry per RadarFrame, panel-sized
	dstY    int           // vertical offset into the panel (same for all frames)
}

// NewSlideRadar returns a SlideFunc that owns its own frame cache.
// Decoded/scaled images are built on first use after each weather refresh
// and reused for all subsequent animation renders — no work is done when
// no clients are connected (the renderer skips calling the slide function).
// use24h controls the clock format in the header.
func NewSlideRadar(use24h bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	cache := &radarFrameCache{}
	return func(dc *gg.Context, data *weather.WeatherData, elapsed, total time.Duration) time.Duration {
		drawRadarSlide(dc, data, elapsed, total, cache, use24h, loc, fonts)
		return 0
	}
}

func drawRadarSlide(dc *gg.Context, data *weather.WeatherData, elapsed, total time.Duration, cache *radarFrameCache, use24h bool, loc *time.Location, fonts *fontSet) {
	drawBackground(dc, "LOCAL RADAR", data.Location, use24h, loc, fonts)

	w := float64(dc.Width())
	h := float64(dc.Height())
	contentTop := headerH + 8

	unavailable := func(msg string) {
		dc.SetFontFace(fonts.medium)
		drawShadowTextAnchored(dc, msg, w/2, h/2, 0.5, 0.5, subR, subG, subB)
	}

	rawFrames := data.RadarFrames
	if len(rawFrames) == 0 {
		if data.StaticMap != nil {
			// Decode and display static basemap as fallback.
			img, err := png.Decode(bytes.NewReader(data.StaticMap))
			if err == nil {
				dstW := dc.Width()
				src := img.Bounds()
				fH := int(float64(src.Dy()) * float64(dstW) / float64(src.Dx()))
				fY := int(contentTop + (h-contentTop-float64(fH))/2)
				if fY < int(contentTop) {
					fY = int(contentTop)
				}
				dst := image.NewRGBA(image.Rect(0, 0, dstW, fH))
				xdraw.BiLinear.Scale(dst, dst.Bounds(), img, src, xdraw.Over, nil)
				dc.DrawImage(dst, 0, fY)
			}
			// Label overlay
			dc.SetFontFace(fonts.medium)
			drawShadowTextAnchored(dc, "NO RADAR DATA — STATIC MAP", w/2, h-30, 0.5, 0.5, subR, subG, subB)
		} else {
			unavailable("RADAR DATA UNAVAILABLE")
		}
		return
	}

	// Rebuild the cache whenever weather data changes (new radar frames fetched).
	// The renderer only calls this when clients are connected, so decode/scale
	// work is never done for idle pipelines.
	if cache.fetched != data.FetchedAt.Unix() {
		dstW := dc.Width()
		scaled := make([]*image.RGBA, 0, len(rawFrames))
		dstY := int(contentTop)
		for _, raw := range rawFrames {
			img, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				continue
			}
			src := img.Bounds()
			fH := int(float64(src.Dy()) * float64(dstW) / float64(src.Dx()))
			fY := int(contentTop + (h-contentTop-float64(fH))/2)
			if fY < int(contentTop) {
				fY = int(contentTop)
			}
			dst := image.NewRGBA(image.Rect(0, 0, dstW, fH))
			xdraw.BiLinear.Scale(dst, dst.Bounds(), img, src, xdraw.Over, nil)
			scaled = append(scaled, dst)
			dstY = fY // all frames are the same size; last write is fine
		}
		cache.scaled = scaled
		cache.dstY = dstY
		cache.fetched = data.FetchedAt.Unix()
	}

	if len(cache.scaled) == 0 {
		unavailable("RADAR DECODE ERROR")
		return
	}

	// Distribute frames evenly across the slide duration.
	frameIdx := 0
	if len(cache.scaled) > 1 && total > 0 {
		progress := float64(elapsed) / float64(total)
		frameIdx = int(progress*float64(len(cache.scaled))) % len(cache.scaled)
	}

	dc.DrawImage(cache.scaled[frameIdx], 0, cache.dstY)

	// Subtle border around the radar image.
	img := cache.scaled[frameIdx]
	dstW := float64(img.Bounds().Dx())
	dstH := float64(img.Bounds().Dy())
	dc.SetRGBA(1, 1, 1, 0.25)
	dc.SetLineWidth(1)
	dc.DrawRectangle(0, float64(cache.dstY), dstW, dstH)
	dc.Stroke()

	// Frame indicator dots (bottom-right corner).
	n := len(cache.scaled)
	if n > 1 {
		dotR := 5.0
		dotSpacing := 14.0
		totalDotsW := float64(n-1)*dotSpacing + dotR*2
		dotY := h - 14.0
		startX := w - totalDotsW - 12.0
		for i := range cache.scaled {
			cx := startX + float64(i)*dotSpacing + dotR
			if i == frameIdx {
				dc.SetRGBA(1, 1, 1, 0.9)
			} else {
				dc.SetRGBA(1, 1, 1, 0.30)
			}
			dc.DrawCircle(cx, dotY, dotR)
			dc.Fill()
		}
	}
}

// satelliteFrameCache holds pre-decoded and pre-scaled satellite images so that
// png.Decode + xdraw.BiLinear.Scale only run once per weather data refresh.
type satelliteFrameCache struct {
	fetched int64
	scaled  []*image.RGBA
	dstY    int
}

// NewSlideSatellite returns a SlideFunc that owns its own frame cache.
// Mirrors NewSlideRadar but uses GOES visible satellite imagery.
func NewSlideSatellite(use24h bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	cache := &satelliteFrameCache{}
	return func(dc *gg.Context, data *weather.WeatherData, elapsed, total time.Duration) time.Duration {
		drawSatelliteSlide(dc, data, elapsed, total, cache, use24h, loc, fonts)
		return 0
	}
}

func drawSatelliteSlide(dc *gg.Context, data *weather.WeatherData, elapsed, total time.Duration, cache *satelliteFrameCache, use24h bool, loc *time.Location, fonts *fontSet) {
	drawBackground(dc, "SATELLITE", data.Location, use24h, loc, fonts)

	w := float64(dc.Width())
	h := float64(dc.Height())
	contentTop := headerH + 8

	unavailable := func(msg string) {
		dc.SetFontFace(fonts.medium)
		drawShadowTextAnchored(dc, msg, w/2, h/2, 0.5, 0.5, subR, subG, subB)
	}

	rawFrames := data.SatelliteFrames
	if len(rawFrames) == 0 {
		unavailable("SATELLITE DATA UNAVAILABLE")
		return
	}

	if cache.fetched != data.FetchedAt.Unix() {
		dstW := dc.Width()
		scaled := make([]*image.RGBA, 0, len(rawFrames))
		dstY := int(contentTop)
		for _, raw := range rawFrames {
			img, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				continue
			}
			src := img.Bounds()
			fH := int(float64(src.Dy()) * float64(dstW) / float64(src.Dx()))
			fY := int(contentTop + (h-contentTop-float64(fH))/2)
			if fY < int(contentTop) {
				fY = int(contentTop)
			}
			dst := image.NewRGBA(image.Rect(0, 0, dstW, fH))
			xdraw.BiLinear.Scale(dst, dst.Bounds(), img, src, xdraw.Over, nil)
			scaled = append(scaled, dst)
			dstY = fY
		}
		cache.scaled = scaled
		cache.dstY = dstY
		cache.fetched = data.FetchedAt.Unix()
	}

	if len(cache.scaled) == 0 {
		unavailable("SATELLITE DECODE ERROR")
		return
	}

	frameIdx := 0
	if len(cache.scaled) > 1 && total > 0 {
		progress := float64(elapsed) / float64(total)
		frameIdx = int(progress*float64(len(cache.scaled))) % len(cache.scaled)
	}

	dc.DrawImage(cache.scaled[frameIdx], 0, cache.dstY)

	img := cache.scaled[frameIdx]
	dstW := float64(img.Bounds().Dx())
	dstH := float64(img.Bounds().Dy())
	dc.SetRGBA(1, 1, 1, 0.25)
	dc.SetLineWidth(1)
	dc.DrawRectangle(0, float64(cache.dstY), dstW, dstH)
	dc.Stroke()

	n := len(cache.scaled)
	if n > 1 {
		dotR := 5.0
		dotSpacing := 14.0
		totalDotsW := float64(n-1)*dotSpacing + dotR*2
		dotY := h - 14.0
		startX := w - totalDotsW - 12.0
		for i := range cache.scaled {
			cx := startX + float64(i)*dotSpacing + dotR
			if i == frameIdx {
				dc.SetRGBA(1, 1, 1, 0.9)
			} else {
				dc.SetRGBA(1, 1, 1, 0.30)
			}
			dc.DrawCircle(cx, dotY, dotR)
			dc.Fill()
		}
	}
}

// NewSlideAnnouncements returns a SlideFunc that shows one announcement per
// slide cycle. getAnns is called on each render to get the current list —
// updates made via the admin interface take effect immediately.
// getDur is called on each render to get the current display duration,
// overriding the renderer's default slide duration.
// use24h controls the clock format in the header.
func NewSlideAnnouncements(getAnns func() []ann.Announcement, getDur func() time.Duration, use24h bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	idx := 0
	lastElapsed := time.Duration(-1)
	return func(dc *gg.Context, data *weather.WeatherData, elapsed, total time.Duration) time.Duration {
		drawBackground(dc, "ANNOUNCEMENTS", data.Location, use24h, loc, fonts)

		// Filter to announcements active today (date-specific ones only show
		// on their MM-DD; entries with no date show every day).
		all := getAnns()
		today := time.Now().Format("01-02")
		var anns []ann.Announcement
		for _, a := range all {
			if a.Date == "" || a.Date == today {
				anns = append(anns, a)
			}
		}
		if len(anns) == 0 {
			return 0
		}

		// elapsed resets to near-zero at the start of each new slide cycle.
		// When that happens (lastElapsed was large, elapsed is now small), advance.
		if lastElapsed > elapsed {
			idx = (idx + 1) % len(anns)
		}
		lastElapsed = elapsed

		// Guard against list shrinking since last advance.
		if idx >= len(anns) {
			idx = 0
		}

		cur := anns[idx]

		w := float64(dc.Width())
		h := float64(dc.Height())
		contentTop := headerH + 8
		contentH := h - contentTop

		// Word-wrap. At fontMedium (32pt Inconsolata monospace) ~52 chars fill
		// the full content width minus padding.
		const wrapChars = 52
		lines := wrapText(strings.ToUpper(cur.Text), wrapChars)

		const lineH = 46.0 // px between baselines at fontMedium
		blockH := float64(len(lines)) * lineH
		pad := lineH * 0.75 // baseline offset for first line

		// If text fits, center it; otherwise scroll with pauses at top/bottom.
		var startY float64
		if blockH <= contentH {
			startY = contentTop + (contentH-blockH)/2 + pad
		} else {
			// Scroll range: from text top-aligned to text bottom-aligned.
			topY := contentTop + pad
			bottomY := contentTop + contentH - blockH + pad
			overflow := topY - bottomY // positive px to scroll

			dur := getDur()
			// Reserve 15% pause at top and 15% at bottom; scroll during middle 70%.
			const pauseFrac = 0.15
			progress := elapsed.Seconds() / dur.Seconds()
			switch {
			case progress <= pauseFrac:
				startY = topY
			case progress >= 1.0-pauseFrac:
				startY = bottomY
			default:
				scrollFrac := (progress - pauseFrac) / (1.0 - 2*pauseFrac)
				startY = topY - overflow*scrollFrac
			}

			// Clip to content area so text doesn't overflow into the header.
			dc.DrawRectangle(0, contentTop, w, contentH)
			dc.Clip()
		}

		dc.SetFontFace(fonts.medium)
		for i, line := range lines {
			y := startY + float64(i)*lineH
			drawShadowTextAnchored(dc, line, w/2, y, 0.5, 0.5, textR, textG, textB)
		}
		dc.ResetClip()

		return getDur()
	}
}

// NewSlideTrivia returns a SlideFunc that shows one trivia question per slide
// cycle. The question is shown for the first 65% of the duration; the answer is
// revealed for the remaining 35%. getItems, getDur, and getRandomize are called
// on each render so that admin changes take effect immediately.
// When getRandomize returns true, questions are drawn from a shuffled deck so
// every question appears once before any repeats.
// use24h controls the clock format in the header.
func NewSlideTrivia(getItems func() []trivia.TriviaItem, getDur func() time.Duration, getRandomize func() bool, use24h bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	idx := 0
	lastElapsed := time.Duration(-1)

	// Shuffled-deck state: deck holds the remaining indices in the current
	// shuffle; deckN is the item count the deck was built for (used to detect
	// list changes that require a rebuild).
	var deck []int
	deckN := 0

	// advance picks the next question index given the current item count.
	advance := func(n int) int {
		if !getRandomize() {
			return (idx + 1) % n
		}
		// Rebuild the deck when the item count changes or it's been exhausted.
		if len(deck) == 0 || deckN != n {
			deck = make([]int, n)
			for i := range deck {
				deck[i] = i
			}
			rand.Shuffle(n, func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
			deckN = n
		}
		next := deck[0]
		deck = deck[1:]
		return next
	}

	return func(dc *gg.Context, data *weather.WeatherData, elapsed, total time.Duration) time.Duration {
		drawBackground(dc, "TRIVIA", data.Location, use24h, loc, fonts)

		items := getItems()
		if len(items) == 0 {
			return 0
		}

		// Detect slide restart and advance to the next question.
		if lastElapsed > elapsed {
			idx = advance(len(items))
		}
		lastElapsed = elapsed

		// Guard against list shrinking since last advance.
		if idx >= len(items) {
			idx = 0
			deck = nil // force deck rebuild on next advance
		}

		duration := getDur()
		cur := items[idx]
		w := float64(dc.Width())
		h := float64(dc.Height())
		cx := w / 2
		contentTop := headerH + 8

		threshold := time.Duration(float64(duration) * 0.65)
		if threshold == 0 {
			threshold = 1
		}

		// Find the correct answer letter for multiple-choice items (not T/F).
		var correctLetter string
		allLabels := []string{"A", "B", "C", "D"}
		isTF := len(cur.Choices) == 2
		if n := len(cur.Choices); n >= 3 && n <= 4 {
			for i, c := range cur.Choices {
				if c == cur.Answer {
					correctLetter = allLabels[i]
					break
				}
			}
		}

		if elapsed < threshold {
			// ── Question phase ────────────────────────────────────────────────

			// Header label
			dc.SetFontFace(fonts.title)
			if isTF {
				drawShadowTextAnchored(dc, "TRUE OR FALSE?", cx, contentTop+47, 0.5, 0.5, titleR, titleG, titleB)
			} else {
				drawShadowTextAnchored(dc, "QUESTION:", cx, contentTop+47, 0.5, 0.5, titleR, titleG, titleB)
			}

			if n := len(cur.Choices); n >= 3 && n <= 4 {
				// Multiple-choice: question text near the top, then labeled choices
				qLines := wrapText(strings.ToUpper(cur.Question), 52)
				const qLineH = 42.0
				const choiceLineH = 52.0

				// Total block height: question lines + gap + choice lines
				qBlockH := float64(len(qLines)) * qLineH
				choicesH := float64(n) * choiceLineH
				totalBlockH := qBlockH + 30.0 + choicesH

				qAreaTop := contentTop + 100.0
				qAreaBot := h - 36.0
				contentAvail := qAreaBot - qAreaTop

				var scrollY float64
				clipped := false
				if totalBlockH <= contentAvail {
					scrollY = qAreaTop
				} else {
					overflow := totalBlockH - contentAvail
					qProgress := elapsed.Seconds() / threshold.Seconds()
					const pauseFrac = 0.15
					switch {
					case qProgress <= pauseFrac:
						scrollY = qAreaTop
					case qProgress >= 1.0-pauseFrac:
						scrollY = qAreaTop - overflow
					default:
						frac := (qProgress - pauseFrac) / (1.0 - 2*pauseFrac)
						scrollY = qAreaTop - overflow*frac
					}
					dc.DrawRectangle(0, contentTop+60, w, qAreaBot-contentTop-60)
					dc.Clip()
					clipped = true
				}

				dc.SetFontFace(fonts.medium)
				for i, line := range qLines {
					drawShadowTextAnchored(dc, line, cx, scrollY+float64(i)*qLineH, 0.5, 0.5, textR, textG, textB)
				}

				// Draw choices, horizontally centered based on the widest choice
				choiceTop := scrollY + qBlockH + 30.0
				const choiceCircleSize = 36
				const choiceGap = 14.0 // gap between circle and text

				// Measure the widest choice to compute block width
				dc.SetFontFace(fonts.medium)
				var maxTextW float64
				choiceTexts := make([]string, n)
				for i, choice := range cur.Choices {
					choiceTexts[i] = strings.ToUpper(choice)
					if len(choiceTexts[i]) > 46 {
						choiceTexts[i] = choiceTexts[i][:43] + "..."
					}
					if tw, _ := dc.MeasureString(choiceTexts[i]); tw > maxTextW {
						maxTextW = tw
					}
				}
				blockW := float64(choiceCircleSize) + choiceGap + maxTextW
				choiceX := (w - blockW) / 2

				for i := range cur.Choices {
					y := choiceTop + float64(i)*choiceLineH
					icon := circledLetter(allLabels[i], choiceCircleSize)
					dc.DrawImageAnchored(icon, int(choiceX+float64(choiceCircleSize)/2), int(y+5), 0.5, 0.5)
					dc.SetFontFace(fonts.medium)
					drawShadowTextAnchored(dc, choiceTexts[i], choiceX+float64(choiceCircleSize)+choiceGap, y, 0.0, 0.5, textR, textG, textB)
				}
				if clipped {
					dc.ResetClip()
				}
			} else {
				// Q&A format: question text centered in the available area
				qLines := wrapText(strings.ToUpper(cur.Question), 52)
				const qLineH = 46.0
				qBlockH := float64(len(qLines)) * qLineH
				pad := qLineH * 0.75
				qAreaTop := contentTop + 80.0
				qAreaBot := h - 36.0
				contentAvail := qAreaBot - qAreaTop

				var qStartY float64
				clipped := false
				if qBlockH <= contentAvail {
					qStartY = qAreaTop + (contentAvail-qBlockH)/2 + pad
				} else {
					topY := qAreaTop + pad
					bottomY := qAreaTop + contentAvail - qBlockH + pad
					overflow := topY - bottomY
					qProgress := elapsed.Seconds() / threshold.Seconds()
					const pauseFrac = 0.15
					switch {
					case qProgress <= pauseFrac:
						qStartY = topY
					case qProgress >= 1.0-pauseFrac:
						qStartY = bottomY
					default:
						frac := (qProgress - pauseFrac) / (1.0 - 2*pauseFrac)
						qStartY = topY - overflow*frac
					}
					dc.DrawRectangle(0, qAreaTop, w, contentAvail)
					dc.Clip()
					clipped = true
				}

				dc.SetFontFace(fonts.medium)
				for i, line := range qLines {
					drawShadowTextAnchored(dc, line, cx, qStartY+float64(i)*qLineH, 0.5, 0.5, textR, textG, textB)
				}
				if clipped {
					dc.ResetClip()
				}
			}

			// Countdown timer — bottom right, just above the progress bar
			remaining := threshold - elapsed
			if remaining < 0 {
				remaining = 0
			}
			secs := int(remaining.Seconds()) + 1
			if remaining == 0 {
				secs = 0
			}
			dc.SetFontFace(fonts.small)
			drawShadowTextAnchored(dc, fmt.Sprintf("%d", secs), w-50, 690, 0.5, 0.5, subR, subG, subB)

			// Progress bar — fills left-to-right showing time until answer reveal
			progress := math.Min(float64(elapsed)/float64(threshold), 1.0)
			const barX, barY, barH = 40.0, 706.0, 8.0
			barW := w - 80
			dc.SetRGBA(1, 1, 1, 0.15)
			dc.DrawRoundedRectangle(barX, barY, barW, barH, barH/2)
			dc.Fill()
			dc.SetRGB(divR, divG, divB)
			dc.DrawRoundedRectangle(barX, barY, barW*progress, barH, barH/2)
			dc.Fill()

		} else {
			// ── Answer phase ──────────────────────────────────────────────────

			// "ANSWER:" header — same position as the question-phase header
			dc.SetFontFace(fonts.title)
			drawShadowTextAnchored(dc, "ANSWER:", cx, contentTop+47, 0.5, 0.5, titleR, titleG, titleB)

			if n := len(cur.Choices); n >= 3 && n <= 4 {
				// MC answer: same layout as question phase, correct choice yellow, others gray
				qLines := wrapText(strings.ToUpper(cur.Question), 52)
				const qLineH = 42.0
				const choiceLineH = 52.0

				qBlockH := float64(len(qLines)) * qLineH
				choicesH := float64(n) * choiceLineH
				totalBlockH := qBlockH + 30.0 + choicesH

				qAreaTop := contentTop + 100.0
				qAreaBot := h - 36.0
				contentAvail := qAreaBot - qAreaTop

				// Static positioning — no scrolling on answer slide
				var startY float64
				if totalBlockH <= contentAvail {
					startY = qAreaTop
				} else {
					startY = qAreaTop // clamp to top if it overflows
				}

				// Question text
				dc.SetFontFace(fonts.medium)
				for i, line := range qLines {
					drawShadowTextAnchored(dc, line, cx, startY+float64(i)*qLineH, 0.5, 0.5, textR, textG, textB)
				}

				// Choices — same layout as question phase
				choiceTop := startY + qBlockH + 30.0
				const choiceCircleSize = 36
				const choiceGap = 14.0

				// Measure the widest choice to compute block width (same as question phase)
				dc.SetFontFace(fonts.medium)
				var maxTextW float64
				choiceTexts := make([]string, n)
				for i, choice := range cur.Choices {
					choiceTexts[i] = strings.ToUpper(choice)
					if len(choiceTexts[i]) > 46 {
						choiceTexts[i] = choiceTexts[i][:43] + "..."
					}
					if tw, _ := dc.MeasureString(choiceTexts[i]); tw > maxTextW {
						maxTextW = tw
					}
				}
				blockW := float64(choiceCircleSize) + choiceGap + maxTextW
				choiceX := (w - blockW) / 2

				const dimR, dimG, dimB = 0.3, 0.3, 0.3 // dark gray for wrong answers
				for i := range cur.Choices {
					y := choiceTop + float64(i)*choiceLineH
					if allLabels[i] == correctLetter {
						// Correct: circle icon + yellow text
						icon := circledLetter(allLabels[i], choiceCircleSize)
						dc.DrawImageAnchored(icon, int(choiceX+float64(choiceCircleSize)/2), int(y+5), 0.5, 0.5)
						dc.SetFontFace(fonts.medium)
						drawShadowTextAnchored(dc, choiceTexts[i], choiceX+float64(choiceCircleSize)+choiceGap, y, 0.0, 0.5, titleR, titleG, titleB)
					} else {
						// Wrong: no circle icon, dark gray text, same x position as labeled text
						dc.SetFontFace(fonts.medium)
						drawShadowTextAnchored(dc, choiceTexts[i], choiceX+float64(choiceCircleSize)+choiceGap, y, 0.0, 0.5, dimR, dimG, dimB)
					}
				}
			} else {
				// Q&A / True-False: original compact answer layout
				answerText := strings.ToUpper(cur.Answer)
				aLines := wrapText(answerText, 28)
				const aLineH = 76.0
				aBlockH := float64(len(aLines)) * aLineH
				aAreaTop := contentTop + 100.0
				aAreaBot := h - 100.0
				aStartY := aAreaTop + (aAreaBot-aAreaTop-aBlockH)/2 + aLineH*0.5

				dc.SetFontFace(fonts.xl)
				for i, line := range aLines {
					drawShadowTextAnchored(dc, line, cx, aStartY+float64(i)*aLineH, 0.5, 0.5, hlR, hlG, hlB)
				}
			}
		}

		return duration
	}
}

// NewSlideMoonTides returns a SlideFunc that renders the moon phase and,
// for coastal locations, a 24-hour tide prediction chart.
func NewSlideMoonTides(use24h, useMetric bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	return func(dc *gg.Context, data *weather.WeatherData, _, _ time.Duration) time.Duration {
		return slideMoonTides(dc, data, use24h, useMetric, loc, fonts)
	}
}

func slideMoonTides(dc *gg.Context, data *weather.WeatherData, use24h, useMetric bool, loc *time.Location, fonts *fontSet) time.Duration {
	if loc == nil {
		loc = time.Local
	}
	hasTides := data.TideData != nil && len(data.TideData.Predictions) > 0

	title := "MOON PHASE"
	if hasTides {
		title = "MOON & TIDES"
	}
	drawBackground(dc, title, data.Location, use24h, loc, fonts)

	w := float64(dc.Width())
	h := float64(dc.Height())
	contentTop := headerH + 8.0
	moon := data.MoonPhase

	if hasTides {
		// ── Coastal layout: moon left, tides right ──
		midX := w / 2

		// Left column: moon disc + info
		moonSize := 200.0
		moonCX := midX / 2
		moonCY := contentTop + (h-contentTop)*0.30

		drawMoonPhase(dc, moonCX, moonCY, moonSize, moon.Phase)

		infoY := moonCY + moonSize/2 + 40
		dc.SetFontFace(fonts.title)
		drawShadowTextAnchored(dc, strings.ToUpper(moon.Name), moonCX, infoY, 0.5, 0.5, titleR, titleG, titleB)

		dc.SetFontFace(fonts.medium)
		drawShadowTextAnchored(dc, fmt.Sprintf("%.0f%% ILLUMINATED", moon.Illumination*100), moonCX, infoY+40, 0.5, 0.5, textR, textG, textB)
		drawShadowTextAnchored(dc, fmt.Sprintf("DAY %.1f OF %.1f", moon.AgeDays, 29.53), moonCX, infoY+76, 0.5, 0.5, subR, subG, subB)

		// High/low tide times below moon info.
		if len(data.TideData.HiLo) > 0 {
			tideY := infoY + 120.0
			dc.SetFontFace(fonts.small)
			drawShadowTextAnchored(dc, "— TIDES —", moonCX, tideY, 0.5, 0.5, subR, subG, subB)
			tideY += 30.0
			timeFmt := "3:04 PM"
			if use24h {
				timeFmt = "15:04"
			}
			now := time.Now()
			for _, e := range data.TideData.HiLo {
				kind := "HIGH"
				r, g, b := textR, textG, textB
				if e.Type == "L" {
					kind = "LOW"
					r, g, b = lowR, lowG, lowB
				}
				label := fmt.Sprintf("%s  %s  %s", kind, e.Time.In(loc).Format(timeFmt), fmtRelTime(e.Time, now))
				dc.SetFontFace(fonts.small)
				drawShadowTextAnchored(dc, label, moonCX, tideY, 0.5, 0.5, r, g, b)
				tideY += 26.0
			}
		}

		// Vertical divider
		dc.SetRGBA(1, 1, 1, 0.20)
		dc.DrawRectangle(midX, contentTop, 2, h-contentTop-20)
		dc.Fill()

		// Right column: tide chart
		td := data.TideData
		preds := td.Predictions

		// Station name
		dc.SetFontFace(fonts.small)
		stationLabel := truncate(strings.ToUpper(td.Station.Name), 30)
		drawShadowTextAnchored(dc, stationLabel, midX+(w-midX)/2, contentTop+28, 0.5, 0.5, subR, subG, subB)

		// Chart bounds
		plotLeft := midX + 70.0
		plotRight := w - 30.0
		plotTop := contentTop + 60.0
		plotBottom := h - 60.0
		plotW := plotRight - plotLeft
		plotH := plotBottom - plotTop

		// Find level range
		minL, maxL := preds[0].Level, preds[0].Level
		for _, p := range preds {
			if p.Level < minL {
				minL = p.Level
			}
			if p.Level > maxL {
				maxL = p.Level
			}
		}
		// Add padding
		pad := (maxL - minL) * 0.15
		if pad < 0.3 {
			pad = 0.3
		}
		minL -= pad
		maxL += pad

		levelToY := func(l float64) float64 {
			return plotBottom - (l-minL)/(maxL-minL)*plotH
		}
		n := len(preds)
		idxToX := func(i int) float64 {
			if n <= 1 {
				return plotLeft + plotW/2
			}
			return plotLeft + float64(i)*plotW/float64(n-1)
		}

		// Y-axis grid lines
		const gridLines = 4
		unitLabel := "FT"
		for g := 0; g <= gridLines; g++ {
			gLevel := minL + float64(g)*(maxL-minL)/float64(gridLines)
			gY := levelToY(gLevel)
			dc.SetRGBA(1, 1, 1, 0.07)
			dc.SetLineWidth(1)
			dc.DrawLine(plotLeft, gY, plotRight, gY)
			dc.Stroke()

			displayLevel := gLevel
			if useMetric {
				displayLevel = gLevel * 0.3048
				unitLabel = "M"
			}
			dc.SetFontFace(fonts.small)
			drawShadowTextAnchored(dc, fmt.Sprintf("%.1f", displayLevel), plotLeft-6, gY, 1.0, 0.5, subR, subG, subB)
		}

		// Unit label at top of Y-axis
		dc.SetFontFace(fonts.small)
		drawShadowTextAnchored(dc, unitLabel, plotLeft-6, plotTop-14, 1.0, 0.5, subR, subG, subB)

		// Area fill under curve
		xs := make([]float64, n)
		ys := make([]float64, n)
		for i := range preds {
			xs[i] = idxToX(i)
			ys[i] = levelToY(preds[i].Level)
		}

		if n > 1 {
			dc.SetRGBA(divR, divG, divB, 0.12)
			dc.MoveTo(xs[0], plotBottom)
			dc.LineTo(xs[0], ys[0])
			for i := 1; i < n; i++ {
				dc.LineTo(xs[i], ys[i])
			}
			dc.LineTo(xs[n-1], plotBottom)
			dc.ClosePath()
			dc.Fill()

			// Line
			dc.SetRGB(divR, divG, divB)
			dc.SetLineWidth(2.5)
			dc.MoveTo(xs[0], ys[0])
			for i := 1; i < n; i++ {
				dc.LineTo(xs[i], ys[i])
			}
			dc.Stroke()
		}

		// Time labels on X axis
		dc.SetFontFace(fonts.small)
		for i, p := range preds {
			// Show every 4th label to avoid crowding
			if i%4 != 0 && i != n-1 {
				continue
			}
			label := formatTideTime(p.Time, use24h)
			drawShadowTextAnchored(dc, label, xs[i], plotBottom+20, 0.5, 0.5, textR, textG, textB)
		}

	} else {
		// ── Inland layout: full-width moon ──
		moonSize := 350.0
		moonCX := w / 2
		moonCY := contentTop + (h-contentTop)*0.38

		drawMoonPhase(dc, moonCX, moonCY, moonSize, moon.Phase)

		infoY := moonCY + moonSize/2 + 50
		dc.SetFontFace(fonts.cardTitle)
		drawShadowTextAnchored(dc, strings.ToUpper(moon.Name), moonCX, infoY, 0.5, 0.5, titleR, titleG, titleB)

		dc.SetFontFace(fonts.mediumXL)
		drawShadowTextAnchored(dc, fmt.Sprintf("%.0f%% ILLUMINATED", moon.Illumination*100), moonCX, infoY+55, 0.5, 0.5, textR, textG, textB)

		dc.SetFontFace(fonts.medium)
		drawShadowTextAnchored(dc, fmt.Sprintf("DAY %.1f OF %.1f", moon.AgeDays, 29.53), moonCX, infoY+100, 0.5, 0.5, subR, subG, subB)
	}

	return 0
}

// fmtRelTime returns a short relative-time string like "(in 3h 5m)" or "(2h 10m ago)".
func fmtRelTime(event, now time.Time) string {
	d := event.Sub(now)
	ago := false
	if d < 0 {
		d = -d
		ago = true
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	var s string
	switch {
	case h > 0 && m > 0:
		s = fmt.Sprintf("%dh %dm", h, m)
	case h > 0:
		s = fmt.Sprintf("%dh", h)
	default:
		s = fmt.Sprintf("%dm", m)
	}
	if ago {
		return "(" + s + " ago)"
	}
	return "(in " + s + ")"
}

// formatTideTime formats a tide prediction time for the X-axis.
func formatTideTime(t time.Time, use24h bool) string {
	if use24h {
		return fmt.Sprintf("%d", t.Hour())
	}
	h := t.Hour()
	suffix := "A"
	if h >= 12 {
		suffix = "P"
	}
	if h == 0 {
		h = 12
	} else if h > 12 {
		h -= 12
	}
	return fmt.Sprintf("%d%s", h, suffix)
}

// NewSlidePrecipitation returns a SlideFunc that renders a 12-hour precipitation
// probability bar chart.
func NewSlidePrecipitation(use24h, useMetric bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	return func(dc *gg.Context, data *weather.WeatherData, _, _ time.Duration) time.Duration {
		return slidePrecipitation(dc, data, use24h, useMetric, loc, fonts)
	}
}

func slidePrecipitation(dc *gg.Context, data *weather.WeatherData, use24h, useMetric bool, loc *time.Location, fonts *fontSet) time.Duration {
	drawBackground(dc, "CHANCE OF PRECIPITATION", data.Location, use24h, loc, fonts)

	w := float64(dc.Width())
	h := float64(dc.Height())

	periods := data.HourlyPeriods
	if len(periods) > 12 {
		periods = periods[:12]
	}
	if len(periods) == 0 {
		dc.SetFontFace(fonts.medium)
		drawShadowTextAnchored(dc, "NO DATA AVAILABLE", w/2, h/2, 0.5, 0.5, subR, subG, subB)
		return 0
	}

	// Extract probability values; check if all zero.
	n := len(periods)
	probs := make([]int, n)
	allZero := true
	for i, p := range periods {
		if p.ProbabilityOfPrecipitation.Value != nil {
			probs[i] = *p.ProbabilityOfPrecipitation.Value
			if probs[i] > 0 {
				allZero = false
			}
		}
	}

	if allZero {
		dc.SetFontFace(fonts.medium)
		drawShadowTextAnchored(dc, "NO PRECIPITATION EXPECTED", w/2, h/2, 0.5, 0.5, subR, subG, subB)
		return 0
	}

	// Chart area.
	const (
		plotLeft   = 110.0
		plotRight  = 1250.0
		plotTop    = 180.0
		plotBottom = 620.0
		iconSize   = 50.0
	)
	plotW := plotRight - plotLeft
	plotH := plotBottom - plotTop

	barW := plotW / float64(n) * 0.6
	gap := plotW / float64(n)

	// Y-axis gridlines at 0%, 25%, 50%, 75%, 100%.
	dc.SetFontFace(fonts.small)
	for g := 0; g <= 4; g++ {
		pct := float64(g) * 25.0
		gY := plotBottom - (pct/100.0)*plotH
		dc.SetRGBA(1, 1, 1, 0.07)
		dc.SetLineWidth(1)
		dc.DrawLine(plotLeft, gY, plotRight, gY)
		dc.Stroke()
		drawShadowTextAnchored(dc, fmt.Sprintf("%.0f%%", pct), plotLeft-8, gY, 1.0, 0.5, subR, subG, subB)
	}

	// Bars, icons, and time labels.
	for i, p := range periods {
		cx := plotLeft + float64(i)*gap + gap/2
		barH := (float64(probs[i]) / 100.0) * plotH
		barX := cx - barW/2
		barY := plotBottom - barH

		// Bar
		dc.SetRGB(divR, divG, divB)
		dc.DrawRoundedRectangle(barX, barY, barW, barH, 3)
		dc.Fill()

		// Value label above bar
		if probs[i] > 0 {
			dc.SetFontFace(fonts.small)
			drawShadowTextAnchored(dc, fmt.Sprintf("%d%%", probs[i]), cx, barY-22, 0.5, 0.5, textR, textG, textB)
		}

		// Weather icon above value label
		icon := conditionIcon(p.ShortForecast, p.IsDaytime)
		drawIcon(dc, icon, cx, (headerH+plotTop)/2, iconSize)

		// Time label below X axis
		dc.SetFontFace(fonts.small)
		drawShadowTextAnchored(dc, hourLabel(p.StartTime, use24h, loc, i), cx, plotBottom+24, 0.5, 0.5, textR, textG, textB)
	}

	// 24h precipitation totals summary line.
	dc.SetFontFace(fonts.small)
	summaryY := plotBottom + 54.0
	precipMM := data.PrecipTotal24h
	snowMM := data.SnowTotal24h
	var summary string
	if precipMM <= 0 && snowMM <= 0 {
		summary = "NEXT 24H:  NO PRECIPITATION"
	} else {
		var parts []string
		if precipMM > 0 {
			if useMetric {
				parts = append(parts, fmt.Sprintf("%.1f mm rain", precipMM))
			} else {
				parts = append(parts, fmt.Sprintf("%.2f in rain", precipMM*0.03937))
			}
		}
		if snowMM > 0 {
			if useMetric {
				parts = append(parts, fmt.Sprintf("%.1f mm snow", snowMM))
			} else {
				parts = append(parts, fmt.Sprintf("%.1f in snow", snowMM*0.03937))
			}
		}
		summary = "NEXT 24H:  " + strings.Join(parts, " · ")
	}
	drawShadowTextAnchored(dc, summary, w/2, summaryY, 0.5, 0.5, subR, subG, subB)

	return 0
}

// NewSlideAlerts returns a SlideFunc that renders active weather alerts.
// The slide is skipped entirely by the renderer when no alerts are active.
func NewSlideAlerts(use24h bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	lastElapsed := time.Duration(-1)
	page := 0
	return func(dc *gg.Context, data *weather.WeatherData, elapsed, total time.Duration) time.Duration {
		return slideAlerts(dc, data, use24h, loc, elapsed, &lastElapsed, &page, fonts)
	}
}

func slideAlerts(dc *gg.Context, data *weather.WeatherData, use24h bool, loc *time.Location, elapsed time.Duration, lastElapsed *time.Duration, page *int, fonts *fontSet) time.Duration {
	if loc == nil {
		loc = time.Local
	}
	drawBackground(dc, "WEATHER ALERTS", data.Location, use24h, loc, fonts)

	w := float64(dc.Width())
	h := float64(dc.Height())
	contentTop := headerH + 8.0

	// Page through alerts: 2 per page.
	const alertsPerPage = 2
	nAlerts := len(data.Alerts)
	if nAlerts == 0 {
		return 0
	}
	totalPages := (nAlerts + alertsPerPage - 1) / alertsPerPage

	// Detect slide restart to advance page.
	if *lastElapsed > elapsed {
		*page = (*page + 1) % totalPages
	}
	*lastElapsed = elapsed
	if *page >= totalPages {
		*page = 0
	}

	startIdx := *page * alertsPerPage
	if startIdx >= nAlerts {
		startIdx = 0
		*page = 0
	}
	endIdx := startIdx + alertsPerPage
	if endIdx > nAlerts {
		endIdx = nAlerts
	}
	pageAlerts := data.Alerts[startIdx:endIdx]

	// Severity color band at top.
	maxSeverity := "Minor"
	for _, a := range data.Alerts {
		if severityRank(a.Severity) > severityRank(maxSeverity) {
			maxSeverity = a.Severity
		}
	}
	bandR, bandG, bandB := severityColor(maxSeverity)
	dc.SetRGB(bandR, bandG, bandB)
	dc.DrawRectangle(0, contentTop, w, 6)
	dc.Fill()

	// Render each alert.
	y := contentTop + 30.0
	slotH := (h - y - 20.0) / float64(len(pageAlerts))

	for i, a := range pageAlerts {
		slotTop := y + float64(i)*slotH

		// Event name — bold yellow
		dc.SetFontFace(fonts.title)
		drawShadowText(dc, strings.ToUpper(a.Event), 60, slotTop+36, titleR, titleG, titleB)

		// Headline — white, wrapped
		if a.Headline != "" {
			lines := truncateLines(wrapText(strings.ToUpper(a.Headline), 60), 3)
			dc.SetFontFace(fonts.small)
			for j, line := range lines {
				drawShadowText(dc, line, 60, slotTop+72+float64(j)*26, textR, textG, textB)
			}
		}

		// Expires time
		if !a.Expires.IsZero() {
			timeFmt := "Mon 3:04 PM"
			if use24h {
				timeFmt = "Mon 15:04"
			}
			dc.SetFontFace(fonts.small)
			expiresStr := fmt.Sprintf("EXPIRES: %s", a.Expires.In(loc).Format(timeFmt))
			drawShadowText(dc, expiresStr, 60, slotTop+slotH-20, subR, subG, subB)
		}

		// Divider between alerts
		if i < len(pageAlerts)-1 {
			divY := slotTop + slotH - 4
			dc.SetRGBA(1, 1, 1, 0.15)
			dc.DrawRectangle(40, divY, w-80, 1)
			dc.Fill()
		}
	}

	// Page indicator if multiple pages.
	if totalPages > 1 {
		dc.SetFontFace(fonts.small)
		pageLabel := fmt.Sprintf("%d / %d", *page+1, totalPages)
		drawShadowTextAnchored(dc, pageLabel, w-40, h-16, 1.0, 0.5, subR, subG, subB)
	}

	return 0
}

func severityRank(s string) int {
	switch s {
	case "Extreme":
		return 4
	case "Severe":
		return 3
	case "Moderate":
		return 2
	case "Minor":
		return 1
	default:
		return 0
	}
}

func severityColor(s string) (r, g, b float64) {
	switch s {
	case "Extreme", "Severe":
		return 0.9, 0.1, 0.1 // red
	case "Moderate":
		return 1.0, 0.6, 0.0 // orange
	default:
		return 1.0, 0.9, 0.0 // yellow
	}
}

// truncate cuts s to at most n runes, appending "…" if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// truncateLines caps lines to maxLines, appending "…" to the last visible line
// if any were dropped.
func truncateLines(lines []string, maxLines int) []string {
	if len(lines) <= maxLines {
		return lines
	}
	lines = lines[:maxLines]
	lines[maxLines-1] = lines[maxLines-1] + "…"
	return lines
}

// wrapText wraps s into lines of at most maxLen runes.
func wrapText(s string, maxLen int) []string {
	var lines []string
	words := strings.Fields(s)
	var line strings.Builder
	for _, w := range words {
		if line.Len() > 0 && line.Len()+1+len(w) > maxLen {
			lines = append(lines, line.String())
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(w)
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}

// ──────────────────────────────────────────────────────────────────────────
// Night Sky (Planets) slide
// ──────────────────────────────────────────────────────────────────────────

// NewSlideNightSky returns a SlideFunc that renders a planisphere-style sky
// dome with planet positions and a stats table.
func NewSlideNightSky(use24h, useMetric bool, loc *time.Location, getPlanetLiveAlways func() bool, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	if getPlanetLiveAlways == nil {
		getPlanetLiveAlways = func() bool { return false }
	}
	return func(dc *gg.Context, data *weather.WeatherData, _, _ time.Duration) time.Duration {
		return slideNightSky(dc, data, use24h, loc, getPlanetLiveAlways, fonts)
	}
}

// Planet color palette (R, G, B as 0–1 floats).
var planetColors = map[string][3]float64{
	"Mercury": {0.75, 0.75, 0.75}, // light gray
	"Venus":   {1.0, 1.0, 0.85},   // bright yellow-white
	"Mars":    {1.0, 0.4, 0.3},    // reddish
	"Jupiter": {1.0, 0.95, 0.85},  // warm white
	"Saturn":  {0.95, 0.90, 0.70}, // pale gold
}

func slideNightSky(dc *gg.Context, data *weather.WeatherData, use24h bool, loc *time.Location, getPlanetLiveAlways func() bool, fonts *fontSet) time.Duration {
	if loc == nil {
		loc = time.Local
	}
	drawBackground(dc, "Night Sky", data.Location, use24h, loc, fonts)

	w := float64(dc.Width())
	h := float64(dc.Height())
	contentTop := headerH + 8.0
	contentH := h - contentTop - 10.0
	midX := w / 2

	if data.Planets == nil {
		dc.SetFontFace(fonts.medium)
		drawShadowTextAnchored(dc, "PLANET DATA UNAVAILABLE", w/2, h/2, 0.5, 0.5, subR, subG, subB)
		return 0
	}

	// Pick which set of positions to display.
	// Use sunset positions when: before sunset AND not live-always AND sunset data exists.
	liveAlways := getPlanetLiveAlways()
	useSunset := !liveAlways && data.Planets.BeforeSunset && len(data.Planets.SunsetPlanets) > 0
	planets := data.Planets.LivePlanets
	if useSunset {
		planets = data.Planets.SunsetPlanets
	}
	sorted := weather.SortPlanetsByAltitude(planets)

	// ── Left half: Sky dome ──
	domeR := contentH * 0.40
	domeCX := midX / 2
	domeCY := contentTop + contentH*0.45

	// Horizon circle (dome edge).
	dc.SetRGBA(1, 1, 1, 0.15)
	dc.DrawCircle(domeCX, domeCY, domeR)
	dc.Stroke()

	// Altitude grid rings at 30° and 60°.
	dc.SetRGBA(1, 1, 1, 0.08)
	dc.DrawCircle(domeCX, domeCY, domeR*2.0/3.0) // 30° alt
	dc.Stroke()
	dc.DrawCircle(domeCX, domeCY, domeR*1.0/3.0) // 60° alt
	dc.Stroke()

	// Cross-hairs (subtle).
	dc.SetRGBA(1, 1, 1, 0.08)
	dc.DrawLine(domeCX-domeR, domeCY, domeCX+domeR, domeCY)
	dc.Stroke()
	dc.DrawLine(domeCX, domeCY-domeR, domeCX, domeCY+domeR)
	dc.Stroke()

	// Compass labels — planisphere style (looking up): N top, S bottom, E left, W right.
	dc.SetFontFace(fonts.small)
	labelOff := domeR + 18
	drawShadowTextAnchored(dc, "N", domeCX, domeCY-labelOff, 0.5, 1.0, subR, subG, subB)
	drawShadowTextAnchored(dc, "S", domeCX, domeCY+labelOff, 0.5, 0.0, subR, subG, subB)
	drawShadowTextAnchored(dc, "E", domeCX-labelOff, domeCY, 1.0, 0.5, subR, subG, subB)
	drawShadowTextAnchored(dc, "W", domeCX+labelOff, domeCY, 0.0, 0.5, subR, subG, subB)

	// Plot planets above horizon on the dome.
	for _, p := range planets {
		if !p.IsUp {
			continue
		}
		// Convert alt/az to dome coordinates.
		// Altitude: 90° = center, 0° = edge. Distance from center = (90-alt)/90 * domeR.
		dist := (90.0 - p.Altitude) / 90.0 * domeR
		// Azimuth: 0=N (up), 90=E. For planisphere (looking up), E is LEFT.
		// Angle from top, clockwise in sky = counterclockwise on dome.
		azRad := -p.Azimuth * math.Pi / 180.0
		px := domeCX + dist*math.Sin(azRad)
		py := domeCY - dist*math.Cos(azRad)

		col := planetColors[p.Name]

		// Dot size proportional to brightness (lower magnitude = brighter = bigger).
		dotR := math.Max(3, 8-p.Magnitude*1.2)

		// Glow.
		dc.SetRGBA(col[0], col[1], col[2], 0.3)
		dc.DrawCircle(px, py, dotR+3)
		dc.Fill()

		// Solid dot.
		dc.SetRGB(col[0], col[1], col[2])
		dc.DrawCircle(px, py, dotR)
		dc.Fill()

		// Label.
		dc.SetFontFace(fonts.small)
		drawShadowText(dc, p.Name[:3], px+dotR+4, py+5, col[0], col[1], col[2])
	}

	// ── Vertical divider ──
	dc.SetRGBA(1, 1, 1, 0.20)
	dc.DrawRectangle(midX, contentTop, 2, contentH)
	dc.Fill()

	// ── Right half: Stats table ──
	tableLeft := midX + 30
	tableRight := w - 30

	// Column positions.
	colName := tableLeft
	colMag := tableLeft + (tableRight-tableLeft)*0.38
	colAlt := tableLeft + (tableRight-tableLeft)*0.58
	colDir := tableLeft + (tableRight-tableLeft)*0.78

	// Header row.
	rowY := contentTop + 35.0
	dc.SetFontFace(fonts.small)
	drawShadowText(dc, "PLANET", colName, rowY, subR, subG, subB)
	drawShadowTextAnchored(dc, "MAG", colMag, rowY, 0.5, 0.5, subR, subG, subB)
	drawShadowTextAnchored(dc, "ALT", colAlt, rowY, 0.5, 0.5, subR, subG, subB)
	drawShadowTextAnchored(dc, "DIR", colDir, rowY, 0.5, 0.5, subR, subG, subB)

	// Divider line.
	rowY += 12
	dc.SetRGBA(1, 1, 1, 0.15)
	dc.DrawRectangle(tableLeft, rowY, tableRight-tableLeft, 1)
	dc.Fill()
	rowY += 18

	// Planet rows (sorted by altitude, highest first).
	dc.SetFontFace(fonts.medium)
	timeFmt := "3:04 PM"
	if use24h {
		timeFmt = "15:04"
	}

	for _, p := range sorted {
		r, g, b := 1.0, 1.0, 1.0
		if !p.IsUp {
			r, g, b = 0.45, 0.45, 0.45
		}

		col := planetColors[p.Name]
		if p.IsUp {
			r, g, b = col[0], col[1], col[2]
		}

		// Name with planet color indicator.
		drawShadowText(dc, strings.ToUpper(p.Name), colName, rowY, r, g, b)

		// Magnitude.
		magStr := fmt.Sprintf("%.1f", p.Magnitude)
		drawShadowTextAnchored(dc, magStr, colMag, rowY, 0.5, 0.5, r, g, b)

		// Altitude & direction.
		if p.IsUp {
			altStr := fmt.Sprintf("%.0f°", p.Altitude)
			drawShadowTextAnchored(dc, altStr, colAlt, rowY, 0.5, 0.5, r, g, b)
			drawShadowTextAnchored(dc, p.Compass, colDir, rowY, 0.5, 0.5, r, g, b)
		} else {
			drawShadowTextAnchored(dc, "--", colAlt, rowY, 0.5, 0.5, r, g, b)
			drawShadowTextAnchored(dc, "--", colDir, rowY, 0.5, 0.5, r, g, b)
		}

		rowY += 38
	}

	// ── Rise/set times ──
	rowY += 15
	dc.SetFontFace(fonts.small)
	// Fixed columns for rise/set table: PLANET  EVENT  TIME  (relative)
	colRSName := colName
	colRSEvent := tableLeft + (tableRight-tableLeft)*0.25
	colRSTime := tableLeft + (tableRight-tableLeft)*0.48
	colRSRel := tableLeft + (tableRight-tableLeft)*0.72
	now := data.Planets.ComputedAt
	for _, p := range sorted {
		hasEvent := false
		col := planetColors[p.Name]
		nameR, nameG, nameB := col[0], col[1], col[2]
		if !p.IsUp {
			nameR, nameG, nameB = 0.45, 0.45, 0.45
		}

		if p.RiseTime != nil {
			drawShadowText(dc, strings.ToUpper(p.Name), colRSName, rowY, nameR, nameG, nameB)
			drawShadowText(dc, "RISES", colRSEvent, rowY, nameR, nameG, nameB)
			drawShadowText(dc, p.RiseTime.In(loc).Format(timeFmt), colRSTime, rowY, nameR, nameG, nameB)
			drawShadowText(dc, fmtRelTime(*p.RiseTime, now), colRSRel, rowY, subR, subG, subB)
			rowY += 22
			hasEvent = true
		}
		if p.SetTime != nil {
			drawShadowText(dc, "", colRSName, rowY, nameR, nameG, nameB)
			drawShadowText(dc, "SETS", colRSEvent, rowY, nameR, nameG, nameB)
			drawShadowText(dc, p.SetTime.In(loc).Format(timeFmt), colRSTime, rowY, nameR, nameG, nameB)
			drawShadowText(dc, fmtRelTime(*p.SetTime, now), colRSRel, rowY, subR, subG, subB)
			rowY += 22
			hasEvent = true
		}
		if hasEvent {
			rowY += 4
		}
		// Stop if running out of vertical space.
		if rowY > h-30 {
			break
		}
	}

	// ── "AT SUNSET" / "LIVE" label — centered at bottom of left (dome) panel ──
	panelCenterX := midX / 2
	labelY := h - 25.0
	dc.SetFontFace(fonts.small)
	if useSunset {
		sunsetFmt := "3:04 PM"
		if use24h {
			sunsetFmt = "15:04"
		}
		label := fmt.Sprintf("AT SUNSET %s", data.Planets.SunsetTime.In(loc).Format(sunsetFmt))
		drawShadowTextAnchored(dc, label, panelCenterX, labelY, 0.5, 0.5, subR, subG, subB)
	} else {
		drawShadowTextAnchored(dc, "LIVE", panelCenterX, labelY, 0.5, 0.5, 0.3, 1.0, 0.3)
	}

	return 0
}

// solarImageCache holds pre-decoded and pre-scaled solar disk images.
type solarImageCache struct {
	fetched int64
	sunspot *image.RGBA
	corona  *image.RGBA
}

// NewSlideSolarWeather returns a SlideFunc for the solar weather slide.
func NewSlideSolarWeather(use24h, useMetric bool, loc *time.Location, fonts *fontSet) SlideFunc {
	if fonts == nil {
		fonts = defaultFonts
	}
	cache := &solarImageCache{}
	return func(dc *gg.Context, data *weather.WeatherData, _, _ time.Duration) time.Duration {
		drawSolarSlide(dc, data, cache, use24h, loc, fonts)
		return 0
	}
}

func drawSolarSlide(dc *gg.Context, data *weather.WeatherData, cache *solarImageCache, use24h bool, loc *time.Location, fonts *fontSet) {
	drawBackground(dc, "Solar Weather", data.Location, use24h, loc, fonts)

	w := float64(dc.Width())
	h := float64(dc.Height())
	contentTop := headerH + 8.0

	if data.Solar == nil {
		dc.SetFontFace(fonts.medium)
		drawShadowTextAnchored(dc, "SOLAR DATA UNAVAILABLE", w/2, h/2, 0.5, 0.5, subR, subG, subB)
		return
	}
	sd := data.Solar

	// Rebuild image cache when data changes.
	if cache.fetched != sd.FetchedAt.Unix() {
		cache.sunspot = decodeSolarImage(sd.SunspotImage)
		cache.corona = decodeSolarImage(sd.CoronaImage)
		cache.fetched = sd.FetchedAt.Unix()
	}

	// Layout: two panels side-by-side, images centred vertically in the content area.
	midX := w / 2
	imgSize := 280.0
	panelCX1 := midX / 2      // center of left panel
	panelCX2 := midX + midX/2 // center of right panel

	// Vertical layout: label + image + 3 stat rows, centred in content area.
	labelH := 24.0 // title label height
	gap1 := 20.0   // gap between label and image
	gap2 := 20.0   // gap between image and stats
	rowH := 28.0
	statsH := rowH * 3
	totalH := labelH + gap1 + imgSize + gap2 + statsH
	contentH := h - contentTop
	startY := contentTop + (contentH-totalH)/2

	labelY := startY + labelH/2
	imgY := startY + labelH + gap1
	statsY := imgY + imgSize + gap2 + 14 // +14 for baseline offset

	// Draw left panel: sunspot image
	dc.SetFontFace(fonts.small)
	drawShadowTextAnchored(dc, "SUNSPOTS", panelCX1, labelY, 0.5, 0.5, titleR, titleG, titleB)
	drawSolarDiskImage(dc, cache.sunspot, panelCX1, imgY, imgSize, fonts)

	// Draw right panel: corona image
	drawShadowTextAnchored(dc, "SOLAR CORONA", panelCX2, labelY, 0.5, 0.5, titleR, titleG, titleB)
	drawSolarDiskImage(dc, cache.corona, panelCX2, imgY, imgSize, fonts)

	// Stats: 3 rows per panel, horizontally centred under each image.
	// Stats block is ~280px wide (matches image), anchored at panel center.
	statsBlockW := imgSize
	leftX := panelCX1 - statsBlockW/2
	drawSolarStat(dc, "KP INDEX", formatKp(sd.KpIndex), kpLabel(sd.KpIndex), leftX, statsY, kpColor(sd.KpIndex), fonts)
	drawSolarStat(dc, "X-RAY", formatXRay(sd.XRayFlux), "", leftX, statsY+rowH, xrayColor(sd.XRayFlux), fonts)
	drawSolarStat(dc, "GEOMAG (G)", fmt.Sprintf("G%d", sd.GeomagScale), noaaScaleLabel(sd.GeomagScale), leftX, statsY+rowH*2, noaaScaleColor(sd.GeomagScale), fonts)

	rightX := panelCX2 - statsBlockW/2
	drawSolarStat(dc, "WIND SPEED", fmt.Sprintf("%.0f km/s", sd.WindSpeedKms), "", rightX, statsY, windColor(sd.WindSpeedKms), fonts)
	drawSolarStat(dc, "RADIO (R)", fmt.Sprintf("R%d", sd.RadioScale), noaaScaleLabel(sd.RadioScale), rightX, statsY+rowH, noaaScaleColor(sd.RadioScale), fonts)
	drawSolarStat(dc, "SOLAR (S)", fmt.Sprintf("S%d", sd.SolarScale), noaaScaleLabel(sd.SolarScale), rightX, statsY+rowH*2, noaaScaleColor(sd.SolarScale), fonts)
}

// decodeSolarImage decodes a JPEG image and scales it to 280x280 with a circular mask.
func decodeSolarImage(data []byte) *image.RGBA {
	if len(data) == 0 {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	const size = 280
	// Scale to size×size.
	scaled := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.BiLinear.Scale(scaled, scaled.Bounds(), img, img.Bounds(), xdraw.Over, nil)

	// Apply circular mask.
	masked := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size) / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - cx + 0.5
			dy := float64(y) - cy + 0.5
			if dx*dx+dy*dy <= r*r {
				masked.Set(x, y, scaled.At(x, y))
			} else {
				masked.Set(x, y, color.RGBA{0, 0, 0, 0})
			}
		}
	}
	return masked
}

// drawSolarDiskImage draws a circular solar image centered at (cx, y) with given size,
// or a placeholder if the image is nil.
func drawSolarDiskImage(dc *gg.Context, img *image.RGBA, cx, y, size float64, fonts *fontSet) {
	if img == nil {
		// Draw placeholder circle
		dc.SetRGBA(1, 1, 1, 0.1)
		dc.DrawCircle(cx, y+size/2, size/2)
		dc.Fill()
		dc.SetFontFace(fonts.small)
		drawShadowTextAnchored(dc, "No data", cx, y+size/2, 0.5, 0.5, subR, subG, subB)
		return
	}
	dc.DrawImage(img, int(cx-size/2), int(y))
}

// drawSolarStat draws a label-value-suffix row for solar metrics.
func drawSolarStat(dc *gg.Context, label, value, suffix string, x, y float64, clr [3]float64, fonts *fontSet) {
	dc.SetFontFace(fonts.small)
	drawShadowText(dc, label, x, y, subR, subG, subB)

	valueX := x + 130
	drawShadowText(dc, value, valueX, y, clr[0], clr[1], clr[2])

	if suffix != "" {
		suffixX := valueX + 50
		drawShadowText(dc, suffix, suffixX, y, clr[0], clr[1], clr[2])
	}
}

// Kp index formatting and color coding.
func formatKp(kp float64) string {
	return fmt.Sprintf("%.1f", kp)
}

func kpLabel(kp float64) string {
	switch {
	case kp < 4:
		return "QUIET"
	case kp < 5:
		return "ACTIVE"
	case kp < 7:
		return "STORM"
	default:
		return "SEVERE"
	}
}

func kpColor(kp float64) [3]float64 {
	switch {
	case kp < 4:
		return [3]float64{0.2, 0.9, 0.2} // green
	case kp < 5:
		return [3]float64{1.0, 1.0, 0.0} // yellow
	case kp < 7:
		return [3]float64{1.0, 0.6, 0.0} // orange
	default:
		return [3]float64{1.0, 0.2, 0.2} // red
	}
}

// X-ray flux formatting and color coding.
func formatXRay(flux string) string {
	if flux == "" {
		return "N/A"
	}
	return flux
}

func xrayColor(flux string) [3]float64 {
	if len(flux) == 0 {
		return [3]float64{subR, subG, subB}
	}
	switch flux[0] {
	case 'X':
		return [3]float64{1.0, 0.2, 0.2} // red
	case 'M':
		return [3]float64{1.0, 0.6, 0.0} // orange
	case 'C':
		return [3]float64{1.0, 1.0, 0.0} // yellow
	default: // A, B
		return [3]float64{0.2, 0.9, 0.2} // green
	}
}

// NOAA R/S/G scale labels and colors.
func noaaScaleLabel(level int) string {
	switch level {
	case 0:
		return "NONE"
	case 1:
		return "MINOR"
	case 2:
		return "MODERATE"
	case 3:
		return "STRONG"
	case 4:
		return "SEVERE"
	case 5:
		return "EXTREME"
	default:
		return ""
	}
}

func noaaScaleColor(level int) [3]float64 {
	switch {
	case level == 0:
		return [3]float64{0.2, 0.9, 0.2} // green
	case level <= 2:
		return [3]float64{1.0, 1.0, 0.0} // yellow
	case level <= 3:
		return [3]float64{1.0, 0.6, 0.0} // orange
	default:
		return [3]float64{1.0, 0.2, 0.2} // red
	}
}

// Solar wind speed color.
func windColor(speed float64) [3]float64 {
	switch {
	case speed < 400:
		return [3]float64{0.2, 0.9, 0.2} // green
	case speed < 500:
		return [3]float64{1.0, 1.0, 0.0} // yellow
	case speed < 700:
		return [3]float64{1.0, 0.6, 0.0} // orange
	default:
		return [3]float64{1.0, 0.2, 0.2} // red
	}
}
