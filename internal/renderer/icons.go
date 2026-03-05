package renderer

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"strings"

	"git.sr.ht/~sbinet/gg"
)

// iconType identifies the weather condition category to draw.
type iconType int

const (
	iconSunny iconType = iota
	iconNightClear
	iconPartlyCloudy
	iconNightPartlyCloudy
	iconMostlyCloudy
	iconCloudy
	iconRain
	iconThunderstorm
	iconSnow
	iconSleet
	iconFog
	iconWindy
	iconHail
	iconTornado
)

// conditionIcon maps a free-text weather description to an iconType.
// isDaytime should reflect whether the period is during daylight hours.
func conditionIcon(desc string, isDaytime bool) iconType {
	s := strings.ToLower(desc)
	switch {
	case anyOf(s, "thunder", "lightning"):
		return iconThunderstorm
	case anyOf(s, "hail"):
		return iconHail
	case anyOf(s, "sleet", "freezing rain", "ice pellet", "wintry mix", "mixed rain", "ice"):
		return iconSleet
	case anyOf(s, "snow", "blizzard", "flurr"):
		return iconSnow
	case anyOf(s, "rain", "shower", "drizzle"):
		return iconRain
	case anyOf(s, "fog", "mist", "haze", "smoke", "dust"):
		return iconFog
	case anyOf(s, "tornado", "funnel"):
		return iconTornado
	case anyOf(s, "wind", "breezy", "blustery"):
		return iconWindy
	case anyOf(s, "mostly cloudy", "considerable cloud", "overcast"):
		return iconMostlyCloudy
	case anyOf(s, "partly cloudy", "partly sunny", "mostly sunny", "partly clear"):
		if isDaytime {
			return iconPartlyCloudy
		}
		return iconNightPartlyCloudy
	case anyOf(s, "cloudy"):
		return iconCloudy
	case anyOf(s, "clear", "sunny", "fair", "bright"):
		if isDaytime {
			return iconSunny
		}
		return iconNightClear
	default:
		if isDaytime {
			return iconPartlyCloudy
		}
		return iconNightPartlyCloudy
	}
}

func anyOf(s string, keywords ...string) bool {
	for _, k := range keywords {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// drawIcon draws the weather icon centered at (cx, cy) scaled to size.
func drawIcon(dc *gg.Context, t iconType, cx, cy, size float64) {
	switch t {
	case iconSunny:
		drawSun(dc, cx, cy, size)
	case iconNightClear:
		drawMoon(dc, cx, cy, size)
	case iconPartlyCloudy:
		drawPartlyCloudy(dc, cx, cy, size, false)
	case iconNightPartlyCloudy:
		drawPartlyCloudy(dc, cx, cy, size, true)
	case iconMostlyCloudy:
		drawMostlyCloudy(dc, cx, cy, size)
	case iconCloudy:
		drawCloudShape(dc, cx, cy, size, 1.0, 1.0, 1.0)
	case iconRain:
		drawRain(dc, cx, cy, size)
	case iconThunderstorm:
		drawThunderstorm(dc, cx, cy, size)
	case iconSnow:
		drawSnow(dc, cx, cy, size)
	case iconSleet:
		drawSleet(dc, cx, cy, size)
	case iconFog:
		drawFog(dc, cx, cy, size)
	case iconWindy:
		drawWindy(dc, cx, cy, size)
	case iconHail:
		drawHail(dc, cx, cy, size)
	case iconTornado:
		drawTornado(dc, cx, cy, size)
	}
}

// DrawLogoSun draws a small sun icon suitable for the logo/header.
// It reuses the same visual pattern as drawSun (yellow disc + 8 rays).
func DrawLogoSun(dc *gg.Context, cx, cy, size float64) {
	drawSun(dc, cx, cy, size)
}

// drawMoonPhase draws a phase-accurate moon disc centred at (cx, cy).
// phase is 0–1 where 0 = new moon, 0.5 = full moon.
// The lit portion is pale yellow-white; the shadow uses the background colour.
func drawMoonPhase(dc *gg.Context, cx, cy, size, phase float64) {
	r := size / 2

	illumination := 0.5 * (1 - math.Cos(2*math.Pi*phase))

	if illumination < 0.02 {
		// New moon — draw a dim outline so something is visible.
		dc.SetRGBA(0.95, 0.95, 0.80, 0.15)
		dc.DrawCircle(cx, cy, r)
		dc.Stroke()
		return
	}

	// Draw the fully lit disc.
	dc.SetRGB(0.95, 0.95, 0.80)
	dc.DrawCircle(cx, cy, r)
	dc.Fill()

	if illumination > 0.98 {
		// Full moon — no shadow needed.
		return
	}

	// Draw shadow overlay. The terminator is an ellipse whose X-radius
	// varies with the phase. We combine a half-disc with this ellipse to
	// produce the correct shadow shape.
	//
	// phase 0–0.5: waxing (shadow on left, receding rightward)
	// phase 0.5–1: waning (shadow on right, growing leftward)

	// terminatorX: the x-radius of the elliptical terminator.
	// At quarter phases (0.25, 0.75) it's 0; at new/full it's r.
	// cos(2π·phase) maps: 0→1, 0.25→0, 0.5→−1, 0.75→0, 1→1
	terminatorX := r * math.Abs(math.Cos(2*math.Pi*phase))

	// Darken shadow slightly from the background so the unlit side is
	// visible against the blue sky.
	dc.SetRGB(bgR*0.5, bgG*0.5, bgB*0.5)

	// Build shadow path at fine resolution.
	const steps = 64
	if phase < 0.5 {
		// Waxing: shadow on the left half.
		// Left edge of shadow is the left half-disc arc (from top to bottom, going left).
		// Right edge is the terminator ellipse.
		dc.NewSubPath()
		for i := 0; i <= steps; i++ {
			a := math.Pi/2 - math.Pi*float64(i)/float64(steps) // π/2 → −π/2
			x := cx - r*math.Cos(a)
			y := cy - r*math.Sin(a)
			if i == 0 {
				dc.MoveTo(x, y)
			} else {
				dc.LineTo(x, y)
			}
		}
		// Terminator: bottom to top on the left side.
		if phase < 0.25 {
			// Before first quarter: terminator bulges right (shadow is wide).
			for i := 0; i <= steps; i++ {
				a := -math.Pi/2 + math.Pi*float64(i)/float64(steps)
				x := cx + terminatorX*math.Cos(a)
				y := cy - r*math.Sin(a)
				dc.LineTo(x, y)
			}
		} else {
			// After first quarter: terminator bulges left (shadow is narrow).
			for i := 0; i <= steps; i++ {
				a := -math.Pi/2 + math.Pi*float64(i)/float64(steps)
				x := cx - terminatorX*math.Cos(a)
				y := cy - r*math.Sin(a)
				dc.LineTo(x, y)
			}
		}
		dc.ClosePath()
		dc.Fill()
	} else {
		// Waning: shadow on the right half.
		dc.NewSubPath()
		for i := 0; i <= steps; i++ {
			a := math.Pi/2 - math.Pi*float64(i)/float64(steps)
			x := cx + r*math.Cos(a)
			y := cy - r*math.Sin(a)
			if i == 0 {
				dc.MoveTo(x, y)
			} else {
				dc.LineTo(x, y)
			}
		}
		if phase < 0.75 {
			// Before last quarter: terminator bulges right (shadow is narrow).
			for i := 0; i <= steps; i++ {
				a := -math.Pi/2 + math.Pi*float64(i)/float64(steps)
				x := cx + terminatorX*math.Cos(a)
				y := cy - r*math.Sin(a)
				dc.LineTo(x, y)
			}
		} else {
			// After last quarter: terminator bulges left (shadow is wide).
			for i := 0; i <= steps; i++ {
				a := -math.Pi/2 + math.Pi*float64(i)/float64(steps)
				x := cx - terminatorX*math.Cos(a)
				y := cy - r*math.Sin(a)
				dc.LineTo(x, y)
			}
		}
		dc.ClosePath()
		dc.Fill()
	}
}

// drawSun draws a bright yellow circle with 8 radiating rays.
func drawSun(dc *gg.Context, cx, cy, size float64) {
	r := size * 0.27
	inner := size * 0.32
	outer := size * 0.40

	// Rays
	dc.SetRGB(hlR, hlG, 0)
	dc.SetLineWidth(size * 0.06)
	for i := 0; i < 8; i++ {
		a := float64(i) * math.Pi / 4
		dc.DrawLine(
			cx+inner*math.Cos(a), cy+inner*math.Sin(a),
			cx+outer*math.Cos(a), cy+outer*math.Sin(a),
		)
		dc.Stroke()
	}

	// Sun disc
	dc.SetRGB(hlR, hlG, 0)
	dc.DrawCircle(cx, cy, r)
	dc.Fill()
}

// drawMoon draws a white crescent moon by tracing only the crescent path,
// leaving the background untouched where the dark side would be.
func drawMoon(dc *gg.Context, cx, cy, size float64) {
	drawCrescent(dc, cx, cy, size*0.24, 0.95, 0.95, 0.80)
}

// drawCrescent traces a crescent moon path (the area inside the moon disc
// but outside an offset overlay disc of equal radius) and fills it.
func drawCrescent(dc *gg.Context, cx, cy, r, cr, cg, cb float64) {
	// Overlay offset — controls crescent thickness.
	ox, oy := r*0.55, -r*0.15
	d := math.Hypot(ox, oy)

	// Angles where the two equal-radius circles intersect.
	lineAngle := math.Atan2(oy, ox)
	halfAngle := math.Acos(d / (2 * r))

	moonUpper := lineAngle + halfAngle
	moonLower := lineAngle - halfAngle
	overUpper := lineAngle + math.Pi - halfAngle
	overLower := lineAngle + math.Pi + halfAngle

	const steps = 64
	dc.NewSubPath()

	// Outer arc: moon circle from upper intersection, counter-clockwise
	// through the left side, to lower intersection (the long way).
	sweep := moonLower + 2*math.Pi - moonUpper
	for i := 0; i <= steps; i++ {
		a := moonUpper + sweep*float64(i)/float64(steps)
		dc.LineTo(cx+r*math.Cos(a), cy+r*math.Sin(a))
	}

	// Inner arc: overlay circle from lower intersection back to upper
	// (the short way, clockwise).
	sweep2 := overUpper - overLower
	for i := 0; i <= steps; i++ {
		a := overLower + sweep2*float64(i)/float64(steps)
		dc.LineTo(cx+ox+r*math.Cos(a), cy+oy+r*math.Sin(a))
	}

	dc.ClosePath()
	dc.SetRGB(cr, cg, cb)
	dc.Fill()
}

// drawCloudShape draws a cloud made of overlapping circles with a flat base.
// r/g/b control the cloud color (white for normal, grays for overcast).
func drawCloudShape(dc *gg.Context, cx, cy, size float64, r, g, b float64) {
	// Anchor the flat base of the cloud at baseY.
	baseY := cy + size*0.12

	type circ struct{ x, y, r float64 }
	circles := []circ{
		{cx - size*0.26, baseY + size*0.00, size * 0.155}, // left, lower
		{cx - size*0.08, baseY - size*0.13, size * 0.230}, // center-left, tallest
		{cx + size*0.12, baseY - size*0.06, size * 0.195}, // center-right
		{cx + size*0.28, baseY + size*0.01, size * 0.145}, // right, lower
	}

	dc.SetRGB(r, g, b)
	for _, c := range circles {
		dc.DrawCircle(c.x, c.y, c.r)
		dc.Fill()
	}

	// Fill shape at the bottom to unify the cloud silhouette into a flat base
	// with rounded bottom corners.
	rectL := circles[0].x - circles[0].r + size*0.03
	rectR := circles[len(circles)-1].x + circles[len(circles)-1].r - size*0.03
	rectTop := baseY - size*0.01
	rectBot := baseY + size*0.16
	corner := size * 0.06

	dc.NewSubPath()
	dc.MoveTo(rectL, rectTop)
	dc.LineTo(rectR, rectTop)
	dc.LineTo(rectR, rectBot-corner)
	dc.QuadraticTo(rectR, rectBot, rectR-corner, rectBot)
	dc.LineTo(rectL+corner, rectBot)
	dc.QuadraticTo(rectL, rectBot, rectL, rectBot-corner)
	dc.ClosePath()
	dc.Fill()
}

// drawPartlyCloudy draws a sun or moon peeking behind a cloud.
func drawPartlyCloudy(dc *gg.Context, cx, cy, size float64, night bool) {
	// Sun/moon sits upper-right, partially revealed behind the cloud.
	bx := cx + size*0.12
	by := cy - size*0.12
	br := size * 0.22

	if night {
		// Moon crescent — only the lit part is drawn.
		drawCrescent(dc, bx, by, br, 0.95, 0.95, 0.80)
	} else {
		// Rays
		dc.SetRGB(hlR, hlG, 0)
		dc.SetLineWidth(size * 0.045)
		inner := br + size*0.03
		outer := br + size*0.10
		for i := 0; i < 8; i++ {
			a := float64(i) * math.Pi / 4
			dc.DrawLine(
				bx+inner*math.Cos(a), by+inner*math.Sin(a),
				bx+outer*math.Cos(a), by+outer*math.Sin(a),
			)
			dc.Stroke()
		}
		// Sun disc
		dc.SetRGB(hlR, hlG, 0)
		dc.DrawCircle(bx, by, br)
		dc.Fill()
	}

	// Cloud in front, shifted lower-left of center.
	drawCloudShape(dc, cx-size*0.10, cy+size*0.08, size*0.76, 1, 1, 1)
}

// drawMostlyCloudy draws a large gray cloud with a small sun peeking from the top-right.
func drawMostlyCloudy(dc *gg.Context, cx, cy, size float64) {
	// Small sun peek
	sunR := size * 0.15
	sunX := cx + size*0.22
	sunY := cy - size*0.15
	dc.SetRGB(hlR, hlG, 0)
	dc.DrawCircle(sunX, sunY, sunR)
	dc.Fill()

	// Dominant gray cloud
	drawCloudShape(dc, cx-size*0.04, cy+size*0.07, size*0.90, 0.78, 0.80, 0.84)
}

// drawRain draws a cloud with three diagonal rain streaks below it.
func drawRain(dc *gg.Context, cx, cy, size float64) {
	cloudCY := cy - size*0.10
	drawCloudShape(dc, cx, cloudCY, size*0.85, 0.65, 0.68, 0.78)

	dc.SetRGB(divR, divG, divB)
	dc.SetLineWidth(size * 0.06)
	dropTopY := cloudCY + size*0.27
	// All streaks share the same angle. Outer streaks are shorter.
	// Shifted slightly right to align with the cloud base visual center.
	type streak struct{ dx, length float64 }
	outerSlant := size * 0.05
	spacing := size * 0.15
	centerLen := size * 0.22
	outerLen := size * 0.16
	centerSlant := outerSlant * centerLen / outerLen
	centerDx := centerSlant - outerSlant // keeps tops equidistant
	nudge := size * 0.04                 // align with cloud base center
	streaks := []streak{
		{-spacing + nudge, outerLen},
		{centerDx + nudge, centerLen},
		{spacing + nudge, outerLen},
	}
	for _, s := range streaks {
		slant := outerSlant * s.length / outerLen
		dc.DrawLine(cx+s.dx-slant, dropTopY, cx+s.dx+slant, dropTopY+s.length)
		dc.Stroke()
	}
}

// drawThunderstorm draws a dark cloud, a yellow lightning bolt, and rain streaks.
func drawThunderstorm(dc *gg.Context, cx, cy, size float64) {
	cloudCY := cy - size*0.14
	drawCloudShape(dc, cx, cloudCY, size*0.85, 0.45, 0.45, 0.55)

	// Rain streaks — same style as drawRain (equidistant, nudged right).
	boltY := cloudCY + size*0.27
	stormSlant := size * 0.04
	stormSpacing := size * 0.15
	stormLen := size * 0.14
	stormNudge := size * 0.04

	dc.SetRGB(divR, divG, divB)
	dc.SetLineWidth(size * 0.055)
	lx := cx - stormSpacing + stormNudge
	rx := cx + stormSpacing + stormNudge
	dc.DrawLine(lx-stormSlant, boltY, lx+stormSlant, boltY+stormLen)
	dc.Stroke()
	dc.DrawLine(rx-stormSlant, boltY, rx+stormSlant, boltY+stormLen)
	dc.Stroke()

	// Lightning bolt centered between the streaks, slanting same direction as rain.
	boltCX := (lx + rx) / 2
	dc.SetRGB(hlR, hlG, 0)
	dc.SetLineWidth(size * 0.06)
	dc.MoveTo(boltCX-size*0.04, boltY)
	dc.LineTo(boltCX+size*0.04, boltY+size*0.13)
	dc.LineTo(boltCX-size*0.03, boltY+size*0.13)
	dc.LineTo(boltCX+size*0.06, boltY+size*0.30)
	dc.Stroke()
}

// drawSnow draws a cloud with three snowflake asterisks beneath it.
func drawSnow(dc *gg.Context, cx, cy, size float64) {
	cloudCY := cy - size*0.10
	drawCloudShape(dc, cx, cloudCY, size*0.85, 0.78, 0.84, 0.90)

	dc.SetRGB(0.55, 0.78, 1.0)
	dc.SetLineWidth(size * 0.035)
	flakeR := size * 0.05

	// Top row: 3 flakes
	row1Y := cloudCY + size*0.35
	for _, fx := range []float64{cx - size*0.18, cx, cx + size*0.18} {
		for i := 0; i < 3; i++ {
			a := float64(i) * math.Pi / 3
			dc.DrawLine(
				fx+flakeR*math.Cos(a), row1Y+flakeR*math.Sin(a),
				fx-flakeR*math.Cos(a), row1Y-flakeR*math.Sin(a),
			)
			dc.Stroke()
		}
	}

	// Bottom row: 2 flakes, staggered between the top row
	row2Y := row1Y + size*0.16
	for _, fx := range []float64{cx - size*0.09, cx + size*0.09} {
		for i := 0; i < 3; i++ {
			a := float64(i) * math.Pi / 3
			dc.DrawLine(
				fx+flakeR*math.Cos(a), row2Y+flakeR*math.Sin(a),
				fx-flakeR*math.Cos(a), row2Y-flakeR*math.Sin(a),
			)
			dc.Stroke()
		}
	}
}

// drawSleet draws a cloud with alternating rain streaks and ice pellet dots.
func drawSleet(dc *gg.Context, cx, cy, size float64) {
	cloudCY := cy - size*0.10
	drawCloudShape(dc, cx, cloudCY, size*0.85, 0.65, 0.68, 0.72)

	dropTopY := cloudCY + size*0.27

	// Rain streaks symmetric about cloud base center; center has ice pellet.
	sleetSpacing := size * 0.15
	sleetSlant := size * 0.04
	sleetLen := size * 0.16
	sleetNudge := size * 0.04 // align with cloud base center

	// Left: rain streak
	dc.SetRGB(divR, divG, divB)
	dc.SetLineWidth(size * 0.06)
	lx := cx - sleetSpacing + sleetNudge
	dc.DrawLine(lx-sleetSlant, dropTopY, lx+sleetSlant, dropTopY+sleetLen)
	dc.Stroke()

	// Right: rain streak
	rx := cx + sleetSpacing + sleetNudge
	dc.DrawLine(rx-sleetSlant, dropTopY, rx+sleetSlant, dropTopY+sleetLen)
	dc.Stroke()

	// Ice pellet dots angled to match the rain streaks, centered between them.
	midX := (lx + rx) / 2
	pelletR := size * 0.04
	slopeX := (2 * sleetSlant) / sleetLen
	y1 := dropTopY + size*0.06
	y2 := dropTopY + size*0.19
	x1 := midX + (y1-dropTopY)*slopeX - sleetSlant
	x2 := midX + (y2-dropTopY)*slopeX - sleetSlant
	dc.SetRGB(0.80, 0.92, 1.0)
	dc.DrawCircle(x1, y1, pelletR)
	dc.Fill()
	dc.DrawCircle(x2, y2, pelletR)
	dc.Fill()
}

// drawFog draws four rounded horizontal bars of decreasing opacity.
func drawFog(dc *gg.Context, cx, cy, size float64) {
	barH := size * 0.10
	barW := size * 0.65
	spacing := size * 0.18
	topY := cy - size*0.30

	for i := 0; i < 4; i++ {
		alpha := 0.90 - float64(i)*0.15
		w := barW * (1.0 - float64(i)*0.08)
		y := topY + float64(i)*spacing
		dc.SetRGBA(0.82, 0.84, 0.86, alpha)
		dc.DrawRoundedRectangle(cx-w/2, y, w, barH, barH/2)
		dc.Fill()
	}
}

// drawWindy draws three sweeping cubic-bezier curves suggesting strong airflow.
func drawWindy(dc *gg.Context, cx, cy, size float64) {
	dc.SetLineWidth(size * 0.075)

	// One canonical S-curve, scaled horizontally for each line.
	// All lines share the same wave shape; shorter lines just end sooner.
	type wind struct {
		w     float64 // horizontal scale (1.0 = full width)
		yOff  float64 // vertical offset from cy
		alpha float64
	}
	lines := []wind{
		{1.0, -size * 0.20, 1.0},
		{0.78, size * 0.02, 0.80},
		{0.56, size * 0.22, 0.60},
	}

	// Canonical curve: starts at left edge, S-curves right.
	// Defined as offsets from center, scaled by w.
	// All lines start at the same left edge. The canonical S-curve is
	// defined at full width; shorter lines trace the same curve but end sooner.
	fullW := size * 0.72 // total width of the longest line
	startX := cx - size*0.36
	amp := size * 0.12 // wave amplitude

	for _, l := range lines {
		w := fullW * l.w
		yc := cy + l.yOff

		sx := startX
		sy := yc
		// Control points placed at 1/3 and 2/3 of the line's width.
		cp1x := startX + w*0.33
		cp1y := yc - amp
		cp2x := startX + w*0.67
		cp2y := yc + amp
		ex := startX + w
		ey := yc

		dc.SetRGBA(0.78, 0.90, 1.0, l.alpha)
		dc.MoveTo(sx, sy)
		dc.CubicTo(cp1x, cp1y, cp2x, cp2y, ex, ey)
		dc.Stroke()
	}
}

// drawHail draws a gray cloud with five ice-blue hailstones below it.
func drawHail(dc *gg.Context, cx, cy, size float64) {
	cloudCY := cy - size*0.10
	drawCloudShape(dc, cx, cloudCY, size*0.85, 0.65, 0.68, 0.72)

	hailR := size * 0.055
	dc.SetRGB(0.80, 0.92, 1.0)

	// Top row: 3 hailstones
	row1Y := cloudCY + size*0.35
	for _, hx := range []float64{cx - size*0.18, cx, cx + size*0.18} {
		dc.DrawCircle(hx, row1Y, hailR)
		dc.Fill()
	}

	// Bottom row: 2 hailstones, staggered
	row2Y := row1Y + size*0.18
	for _, hx := range []float64{cx - size*0.09, cx + size*0.09} {
		dc.DrawCircle(hx, row2Y, hailR)
		dc.Fill()
	}
}

// drawTornado draws a funnel shape using progressively narrower Bézier curves.
func drawTornado(dc *gg.Context, cx, cy, size float64) {
	dc.SetLineWidth(size * 0.07)

	type funnel struct {
		w     float64 // width scale
		yOff  float64 // vertical offset from cy
		alpha float64
	}
	lines := []funnel{
		{1.0, -size * 0.30, 1.0},
		{0.75, -size * 0.10, 0.90},
		{0.50, size * 0.10, 0.75},
		{0.30, size * 0.25, 0.60},
		{0.15, size * 0.38, 0.45},
	}

	fullW := size * 0.70
	amp := size * 0.08

	for _, l := range lines {
		w := fullW * l.w
		yc := cy + l.yOff
		sx := cx - w/2
		ex := cx + w/2

		cp1x := sx + w*0.33
		cp1y := yc - amp
		cp2x := sx + w*0.67
		cp2y := yc + amp

		dc.SetRGBA(0.78, 0.90, 1.0, l.alpha)
		dc.MoveTo(sx, yc)
		dc.CubicTo(cp1x, cp1y, cp2x, cp2y, ex, yc)
		dc.Stroke()
	}
}

// RenderIconSheet draws all weather condition icons in a grid and saves to path.
func RenderIconSheet(path string) error {
	type entry struct {
		icon  iconType
		label string
	}
	icons := []entry{
		{iconSunny, "Sunny"},
		{iconNightClear, "Night Clear"},
		{iconPartlyCloudy, "Partly Cloudy"},
		{iconNightPartlyCloudy, "Night Partly Cloudy"},
		{iconMostlyCloudy, "Mostly Cloudy"},
		{iconCloudy, "Cloudy"},
		{iconRain, "Rain"},
		{iconThunderstorm, "Thunderstorm"},
		{iconSnow, "Snow"},
		{iconSleet, "Sleet"},
		{iconFog, "Fog"},
		{iconWindy, "Windy"},
		{iconHail, "Hail"},
		{iconTornado, "Tornado"},
	}

	cols := 4
	rows := (len(icons) + cols - 1) / cols
	cellW, cellH := 320.0, 280.0
	w := float64(cols) * cellW
	h := float64(rows) * cellH

	dc := gg.NewContext(int(w), int(h))

	for y := 0; y < int(h); y++ {
		t := float64(y) / h
		r := 0.063*(1-t) + 0.0*t
		g := 0.125*(1-t) + 0.063*t
		b := 0.502*(1-t) + 0.251*t
		dc.SetRGB(r, g, b)
		dc.DrawRectangle(0, float64(y), w, 1)
		dc.Fill()
	}

	iconSize := 160.0
	for i, e := range icons {
		col := i % cols
		row := i / cols
		cx := float64(col)*cellW + cellW/2
		cy := float64(row)*cellH + cellH/2 - 20

		drawIcon(dc, e.icon, cx, cy, iconSize)

		dc.SetRGB(textR, textG, textB)
		dc.SetFontFace(defaultFonts.small)
		dc.DrawStringAnchored(e.label, cx, cy+iconSize/2+20, 0.5, 0.5)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, dc.Image()); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	fmt.Printf("wrote %s (%dx%d)\n", path, int(w), int(h))
	return nil
}
