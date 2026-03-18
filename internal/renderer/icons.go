package renderer

import (
	"fmt"
	"image"
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
	iconNightMostlyCloudy
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
		if isDaytime {
			return iconMostlyCloudy
		}
		return iconNightMostlyCloudy
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
		drawMostlyCloudy(dc, cx, cy, size, false)
	case iconNightMostlyCloudy:
		drawMostlyCloudy(dc, cx, cy, size, true)
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

// drawIconWithMoon is like drawIcon but uses a phase-accurate moon disc
// for night icons when realistic is true, and a fun rainbow sun when funSun is true.
func drawIconWithMoon(dc *gg.Context, t iconType, cx, cy, size, moonPhase float64, realistic, funSun bool) {
	// Handle fun sun for sunny/partly cloudy/mostly cloudy day icons.
	if funSun {
		switch t {
		case iconSunny:
			drawFunSun(dc, cx, cy, size)
			return
		case iconPartlyCloudy:
			drawPartlyCloudyFunSun(dc, cx, cy, size)
			return
		case iconMostlyCloudy:
			drawMostlyCloudyFunSun(dc, cx, cy, size)
			return
		}
	}
	if !realistic || (t != iconNightClear && t != iconNightPartlyCloudy && t != iconNightMostlyCloudy) {
		drawIcon(dc, t, cx, cy, size)
		return
	}
	switch t {
	case iconNightClear:
		drawMoonPhase(dc, cx, cy, size*0.48, moonPhase)
	case iconNightPartlyCloudy:
		drawPartlyCloudyMoon(dc, cx, cy, size, moonPhase)
	case iconNightMostlyCloudy:
		drawMostlyCloudyMoon(dc, cx, cy, size, moonPhase)
	}
}

// drawPartlyCloudyMoon is like drawPartlyCloudy for nighttime but uses
// a phase-accurate moon disc instead of a crescent.
func drawPartlyCloudyMoon(dc *gg.Context, cx, cy, size, moonPhase float64) {
	bx := cx + size*0.12
	by := cy - size*0.12
	br := size * 0.22
	drawMoonPhase(dc, bx, by, br*2, moonPhase)
	drawCloudShape(dc, cx, cy, size, 1.0, 1.0, 1.0)
}

// DrawLogoSun draws a small sun icon suitable for the logo/header.
// It reuses the same visual pattern as drawSun (yellow disc + 8 rays).
func DrawLogoSun(dc *gg.Context, cx, cy, size float64) {
	drawSun(dc, cx, cy, size)
}

// drawMoonPhase draws a phase-accurate moon disc centred at (cx, cy).
// phase is 0–1 where 0 = new moon, 0.5 = full moon.
// The dark side is drawn as a subtle disc first, then the lit portion
// is drawn on top so there is no anti-aliasing outline around the edge.
func drawMoonPhase(dc *gg.Context, cx, cy, size, phase float64) {
	r := size / 2

	illumination := 0.5 * (1 - math.Cos(2*math.Pi*phase))

	if illumination < 0.02 {
		// New moon — draw a dim disc with a faint light ring so the
		// outline is visible against the dark gradient background.
		dc.SetRGBA(0.18, 0.20, 0.30, 0.65)
		dc.DrawCircle(cx, cy, r)
		dc.Fill()
		dc.SetRGBA(0.5, 0.5, 0.55, 0.35)
		dc.SetLineWidth(1.5)
		dc.DrawCircle(cx, cy, r)
		dc.Stroke()
		return
	}

	// Draw the dark side disc first — a subtle dark blue-grey so the
	// unlit portion is visible against the gradient background.
	dc.SetRGBA(0.15, 0.17, 0.28, 0.75)
	dc.DrawCircle(cx, cy, r)
	dc.Fill()

	if illumination > 0.98 {
		// Full moon — draw fully lit disc over the dark base.
		dc.SetRGB(0.95, 0.95, 0.80)
		dc.DrawCircle(cx, cy, r)
		dc.Fill()
		return
	}

	// Draw the lit portion on top. The terminator is an ellipse whose
	// X-radius varies with the phase. We combine a half-disc arc with
	// this ellipse to produce the lit crescent/gibbous shape.
	//
	// phase 0–0.5: waxing (lit on the right)
	// phase 0.5–1: waning (lit on the left)

	// terminatorX: the x-radius of the elliptical terminator.
	// At quarter phases (0.25, 0.75) it's 0; at new/full it's r.
	terminatorX := r * math.Abs(math.Cos(2*math.Pi*phase))

	dc.SetRGB(0.95, 0.95, 0.80)

	const steps = 64
	if phase < 0.5 {
		// Waxing: lit on the right half.
		// Right edge is the right half-disc arc (top to bottom).
		// Left edge is the terminator ellipse.
		dc.NewSubPath()
		for i := 0; i <= steps; i++ {
			a := math.Pi/2 - math.Pi*float64(i)/float64(steps) // π/2 → −π/2
			x := cx + r*math.Cos(a)
			y := cy - r*math.Sin(a)
			if i == 0 {
				dc.MoveTo(x, y)
			} else {
				dc.LineTo(x, y)
			}
		}
		// Terminator: bottom to top.
		if phase < 0.25 {
			// Before first quarter: terminator bulges left (lit area is narrow).
			for i := 0; i <= steps; i++ {
				a := -math.Pi/2 + math.Pi*float64(i)/float64(steps)
				x := cx + terminatorX*math.Cos(a)
				y := cy - r*math.Sin(a)
				dc.LineTo(x, y)
			}
		} else {
			// After first quarter: terminator bulges right (lit area is wide).
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
		// Waning: lit on the left half.
		// Left edge is the left half-disc arc (top to bottom).
		// Right edge is the terminator ellipse.
		dc.NewSubPath()
		for i := 0; i <= steps; i++ {
			a := math.Pi/2 - math.Pi*float64(i)/float64(steps)
			x := cx - r*math.Cos(a)
			y := cy - r*math.Sin(a)
			if i == 0 {
				dc.MoveTo(x, y)
			} else {
				dc.LineTo(x, y)
			}
		}
		if phase < 0.75 {
			// Before last quarter: terminator bulges right (lit area is wide).
			for i := 0; i <= steps; i++ {
				a := -math.Pi/2 + math.Pi*float64(i)/float64(steps)
				x := cx + terminatorX*math.Cos(a)
				y := cy - r*math.Sin(a)
				dc.LineTo(x, y)
			}
		} else {
			// After last quarter: terminator bulges left (lit area is narrow).
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

// drawFunSun draws a sun with rainbow-colored rays.
func drawFunSun(dc *gg.Context, cx, cy, size float64) {
	r := size * 0.27
	inner := size * 0.32
	outer := size * 0.40

	rayColors := [][3]float64{
		{1.0, 0.3, 0.3}, // red
		{1.0, 0.6, 0.2}, // orange
		{1.0, 1.0, 0.3}, // yellow
		{0.4, 1.0, 0.4}, // green
		{0.3, 0.8, 1.0}, // cyan
		{0.4, 0.4, 1.0}, // blue
		{0.7, 0.3, 1.0}, // purple
		{1.0, 0.4, 0.7}, // pink
	}
	dc.SetLineWidth(size * 0.07)
	for i := 0; i < 8; i++ {
		rc := rayColors[i]
		dc.SetRGB(rc[0], rc[1], rc[2])
		a := float64(i) * math.Pi / 4
		dc.DrawLine(
			cx+inner*math.Cos(a), cy+inner*math.Sin(a),
			cx+outer*math.Cos(a), cy+outer*math.Sin(a),
		)
		dc.Stroke()
	}

	// Pale yellow disc.
	dc.SetRGB(1.0, 1.0, 0.6)
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

// drawPartlyCloudyFunSun draws a fun rainbow sun peeking behind a cloud.
func drawPartlyCloudyFunSun(dc *gg.Context, cx, cy, size float64) {
	bx := cx + size*0.12
	by := cy - size*0.12
	drawFunSun(dc, bx, by, size*0.44)
	drawCloudShape(dc, cx-size*0.10, cy+size*0.08, size*0.76, 1, 1, 1)
}

// drawMostlyCloudyFunSun draws a large gray cloud with a fun sun peeking from the top-right.
func drawMostlyCloudyFunSun(dc *gg.Context, cx, cy, size float64) {
	peekX := cx + size*0.22
	peekY := cy - size*0.15
	drawFunSun(dc, peekX, peekY, size*0.30)
	drawCloudShape(dc, cx-size*0.04, cy+size*0.07, size*0.90, 0.78, 0.80, 0.84)
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

// drawMostlyCloudy draws a large gray cloud with a small sun or moon peeking from the top-right.
func drawMostlyCloudy(dc *gg.Context, cx, cy, size float64, night bool) {
	peekR := size * 0.15
	peekX := cx + size*0.22
	peekY := cy - size*0.15

	if night {
		// Moon crescent peek
		drawCrescent(dc, peekX, peekY, peekR, 0.95, 0.95, 0.80)
	} else {
		// Small sun peek
		dc.SetRGB(hlR, hlG, 0)
		dc.DrawCircle(peekX, peekY, peekR)
		dc.Fill()
	}

	// Dominant gray cloud
	drawCloudShape(dc, cx-size*0.04, cy+size*0.07, size*0.90, 0.78, 0.80, 0.84)
}

// drawMostlyCloudyMoon is like drawMostlyCloudy for nighttime but uses
// a phase-accurate moon disc instead of a crescent.
func drawMostlyCloudyMoon(dc *gg.Context, cx, cy, size, moonPhase float64) {
	peekR := size * 0.15
	peekX := cx + size*0.22
	peekY := cy - size*0.15
	drawMoonPhase(dc, peekX, peekY, peekR*2, moonPhase)
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
		{iconNightMostlyCloudy, "Night Mostly Cloudy"},
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

// RenderRealisticIconSheet draws all weather icons using phase-accurate moon
// rendering for night icons and saves to path.
func RenderRealisticIconSheet(path string) error {
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
		{iconNightMostlyCloudy, "Night Mostly Cloudy"},
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
	moonPhase := 0.15 // waxing crescent for a visible phase shape
	for i, e := range icons {
		col := i % cols
		row := i / cols
		cx := float64(col)*cellW + cellW/2
		cy := float64(row)*cellH + cellH/2 - 20

		drawIconWithMoon(dc, e.icon, cx, cy, iconSize, moonPhase, true, false)

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

// RenderFunSunIconSheet draws all weather icons with fun sun (rainbow rays)
// and phase-accurate moon rendering, and saves to path.
func RenderFunSunIconSheet(path string) error {
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
		{iconNightMostlyCloudy, "Night Mostly Cloudy"},
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
	moonPhase := 0.15
	for i, e := range icons {
		col := i % cols
		row := i / cols
		cx := float64(col)*cellW + cellW/2
		cy := float64(row)*cellH + cellH/2 - 20

		drawIconWithMoon(dc, e.icon, cx, cy, iconSize, moonPhase, true, true)

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

// RenderMoonPhaseSheet draws moon phases in a grid and saves to path.
// Shows 16 phases from new moon (0) through full moon (0.5) and back.
func RenderMoonPhaseSheet(path string) error {
	type entry struct {
		phase float64
		label string
	}
	phases := []entry{
		{0.0, "New Moon"},
		{0.0625, "Waxing Crescent 1"},
		{0.125, "Waxing Crescent 2"},
		{0.1875, "Waxing Crescent 3"},
		{0.25, "First Quarter"},
		{0.3125, "Waxing Gibbous 1"},
		{0.375, "Waxing Gibbous 2"},
		{0.4375, "Waxing Gibbous 3"},
		{0.5, "Full Moon"},
		{0.5625, "Waning Gibbous 1"},
		{0.625, "Waning Gibbous 2"},
		{0.6875, "Waning Gibbous 3"},
		{0.75, "Last Quarter"},
		{0.8125, "Waning Crescent 1"},
		{0.875, "Waning Crescent 2"},
		{0.9375, "Waning Crescent 3"},
	}

	cols := 4
	rows := (len(phases) + cols - 1) / cols
	cellW, cellH := 320.0, 280.0
	w := float64(cols) * cellW
	h := float64(rows) * cellH

	dc := gg.NewContext(int(w), int(h))

	// Draw gradient background matching the slide style.
	for y := 0; y < int(h); y++ {
		t := float64(y) / h
		r := 0.063*(1-t) + 0.0*t
		g := 0.125*(1-t) + 0.063*t
		b := 0.502*(1-t) + 0.251*t
		dc.SetRGB(r, g, b)
		dc.DrawRectangle(0, float64(y), w, 1)
		dc.Fill()
	}

	moonSize := 160.0
	for i, e := range phases {
		col := i % cols
		row := i / cols
		cx := float64(col)*cellW + cellW/2
		cy := float64(row)*cellH + cellH/2 - 20

		drawMoonPhase(dc, cx, cy, moonSize, e.phase)

		dc.SetRGB(textR, textG, textB)
		dc.SetFontFace(defaultFonts.small)
		dc.DrawStringAnchored(e.label, cx, cy+moonSize/2+20, 0.5, 0.5)
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

// ────────────────────────────────────────────────────────────────────────────
// Holiday Icons
// ────────────────────────────────────────────────────────────────────────────

// drawHolidayHeart draws a red heart (Valentine's Day).
func drawHolidayHeart(dc *gg.Context, cx, cy, size float64) {
	s := size * 0.35
	dc.SetRGB(0.9, 0.1, 0.2)
	// Heart as a single bezier path — no seams.
	dc.NewSubPath()
	dc.MoveTo(cx, cy+s*0.8) // bottom point
	// Left side.
	dc.CubicTo(cx-s*1.0, cy+s*0.1, cx-s*1.0, cy-s*0.6, cx-s*0.5, cy-s*0.75)
	// Top-left bump to center dip.
	dc.CubicTo(cx-s*0.15, cy-s*0.85, cx, cy-s*0.55, cx, cy-s*0.4)
	// Center dip to top-right bump.
	dc.CubicTo(cx, cy-s*0.55, cx+s*0.15, cy-s*0.85, cx+s*0.5, cy-s*0.75)
	// Right side.
	dc.CubicTo(cx+s*1.0, cy-s*0.6, cx+s*1.0, cy+s*0.1, cx, cy+s*0.8)
	dc.ClosePath()
	dc.Fill()
}

// drawHolidayShamrock draws a green four-leaf clover (St. Patrick's Day).
func drawHolidayShamrock(dc *gg.Context, cx, cy, size float64) {
	// Reuse the proven Valentine heart bezier, but rotated for each leaf.
	// The heart path is defined with the point at bottom and lobes at top,
	// then we rotate so each leaf's point faces the center and lobes face out.
	//
	// In local coords (before rotation): point at (0, +s*0.8), lobes at top.
	// We rotate so the point faces toward (cx,cy) and lobes face outward.
	drawHeartLeaf := func(angle, s float64, r, g, b, a float64) {
		// angle = direction the leaf points OUTWARD from center.
		// The heart template has its point at (0, +s*0.8) pointing down.
		// We rotate so the point faces inward (toward center) and lobes face out.
		rot := angle + math.Pi/2
		cos, sin := math.Cos(rot), math.Sin(rot)

		// Transform local point to world. We squish the X axis (perpendicular
		// to the leaf) to make each leaf narrower/more elongated like a real clover.
		// Offset so the heart's point (local 0, s*0.8) lands at (cx,cy) —
		// lobes extend outward, point sits at center.
		ws := 0.78 // width scale
		offX := s * 0.8 * math.Cos(angle)
		offY := s * 0.8 * math.Sin(angle)
		p := func(lx, ly float64) (float64, float64) {
			slx := lx * ws
			return cx + offX + slx*cos - ly*sin, cy + offY + slx*sin + ly*cos
		}

		// Heart bezier path (same proven shape as drawHolidayHeart).
		bx, by := p(0, s*0.8) // bottom point (will become the center-facing tip)
		dc.NewSubPath()
		dc.MoveTo(bx, by)
		// Left side.
		c1x, c1y := p(-s*1.0, s*0.1)
		c2x, c2y := p(-s*1.0, -s*0.6)
		ex, ey := p(-s*0.5, -s*0.75)
		dc.CubicTo(c1x, c1y, c2x, c2y, ex, ey)
		// Top-left bump to center dip.
		c3x, c3y := p(-s*0.15, -s*0.85)
		c4x, c4y := p(0, -s*0.55)
		dx, dy := p(0, -s*0.4)
		dc.CubicTo(c3x, c3y, c4x, c4y, dx, dy)
		// Center dip to top-right bump.
		c5x, c5y := p(0, -s*0.55)
		c6x, c6y := p(s*0.15, -s*0.85)
		fx, fy := p(s*0.5, -s*0.75)
		dc.CubicTo(c5x, c5y, c6x, c6y, fx, fy)
		// Right side.
		c7x, c7y := p(s*1.0, -s*0.6)
		c8x, c8y := p(s*1.0, s*0.1)
		dc.CubicTo(c7x, c7y, c8x, c8y, bx, by)
		dc.ClosePath()
		if a >= 1.0 {
			dc.SetRGB(r, g, b)
		} else {
			dc.SetRGBA(r, g, b, a)
		}
		dc.Fill()
	}

	s := size * 0.16 // leaf size (scale factor for the heart template)

	// Four leaves at 90° intervals, tilted ~15° clockwise like the emoji.
	tilt := -0.25 // slight clockwise tilt of the whole clover
	leafAngles := []float64{
		-math.Pi*3/4 + tilt, // upper-left
		-math.Pi/4 + tilt,   // upper-right
		math.Pi/4 + tilt,    // lower-right
		math.Pi*3/4 + tilt,  // lower-left
	}
	outerColors := [][3]float64{
		{0.13, 0.52, 0.13},
		{0.06, 0.38, 0.06},
		{0.13, 0.52, 0.13},
		{0.06, 0.38, 0.06},
	}
	innerColors := [][3]float64{
		{0.25, 0.65, 0.22},
		{0.18, 0.55, 0.15},
		{0.25, 0.65, 0.22},
		{0.18, 0.55, 0.15},
	}

	// Draw back leaves first for overlap.
	drawOrder := []int{2, 3, 0, 1}
	for _, i := range drawOrder {
		oc := outerColors[i]
		drawHeartLeaf(leafAngles[i], s, oc[0], oc[1], oc[2], 1.0)
		// Inner marking: smaller heart, offset outward along the leaf axis.
		ic := innerColors[i]
		off := s * 0.35
		savedCx, savedCy := cx, cy
		cx += off * math.Cos(leafAngles[i])
		cy += off * math.Sin(leafAngles[i])
		drawHeartLeaf(leafAngles[i], s*0.45, ic[0], ic[1], ic[2], 0.85)
		cx, cy = savedCx, savedCy
	}

	// Light center dot.
	dc.SetRGBA(0.45, 0.78, 0.40, 0.55)
	dc.DrawCircle(cx, cy, size*0.018)
	dc.Fill()
}

// drawHolidayChampagne draws a champagne flute with bubbles (New Year's Day).
func drawHolidayChampagne(dc *gg.Context, cx, cy, size float64) {
	// Champagne flute — bottom portion of a narrow ellipse (top cut off at the rim).
	fluteTop := cy - size*0.28
	fluteBot := cy + size*0.04
	fluteH := fluteBot - fluteTop
	eRx := size * 0.08             // horizontal radius of the ellipse
	eRy := fluteH * 0.7            // vertical radius — taller than visible portion
	eCY := fluteTop - eRy + fluteH // ellipse center below the rim
	// Draw the visible lower arc of the ellipse (from rim down around the bottom and back).
	dc.SetRGBA(0.8, 0.88, 1.0, 0.45)
	dc.NewSubPath()
	// Find the angle at the rim (where the ellipse intersects fluteTop).
	rimAngle := math.Asin((fluteTop - eCY) / eRy)
	for j := 0; j <= 40; j++ {
		a := rimAngle + (math.Pi-2*rimAngle)*float64(j)/40
		px := cx + eRx*math.Cos(a)
		py := eCY + eRy*math.Sin(a)
		if j == 0 {
			dc.MoveTo(px, py)
		} else {
			dc.LineTo(px, py)
		}
	}
	dc.ClosePath()
	dc.Fill()
	// Rim line.
	rimW := eRx * math.Cos(rimAngle)
	dc.SetRGBA(0.9, 0.92, 1.0, 0.7)
	dc.SetLineWidth(size * 0.01)
	dc.DrawLine(cx-rimW, fluteTop, cx+rimW, fluteTop)
	dc.Stroke()
	// Champagne liquid — fills lower 55%.
	liquidTop := fluteTop + fluteH*0.45
	liquidAngle := math.Asin((liquidTop - eCY) / eRy)
	dc.SetRGBA(1.0, 0.85, 0.3, 0.7)
	dc.NewSubPath()
	for j := 0; j <= 40; j++ {
		a := liquidAngle + (math.Pi-2*liquidAngle)*float64(j)/40
		px := cx + eRx*math.Cos(a)
		py := eCY + eRy*math.Sin(a)
		if j == 0 {
			dc.MoveTo(px, py)
		} else {
			dc.LineTo(px, py)
		}
	}
	dc.ClosePath()
	dc.Fill()
	// Thin stem.
	dc.SetRGBA(0.8, 0.88, 1.0, 0.6)
	dc.SetLineWidth(size * 0.015)
	stemBot := cy + size*0.18
	dc.DrawLine(cx, fluteBot, cx, stemBot)
	dc.Stroke()
	// Small round base.
	dc.SetLineWidth(size * 0.015)
	dc.DrawEllipse(cx, stemBot+size*0.01, size*0.06, size*0.015)
	dc.Stroke()
	// Bubbles rising from glass.
	dc.SetRGBA(1.0, 1.0, 0.85, 0.7)
	bubbles := [][3]float64{
		{cx - size*0.02, fluteTop - size*0.04, size * 0.012},
		{cx + size*0.03, fluteTop - size*0.09, size * 0.01},
		{cx - size*0.01, fluteTop - size*0.15, size * 0.008},
		{cx + size*0.02, fluteTop - size*0.2, size * 0.007},
		{cx, fluteTop - size*0.26, size * 0.006},
	}
	for _, b := range bubbles {
		dc.DrawCircle(b[0], b[1], b[2])
		dc.Fill()
	}
}

// drawHolidayFirework draws a red/white/blue firework with sparkles (Independence Day).
func drawHolidayFirework(dc *gg.Context, cx, cy, size float64) {
	colors := [][3]float64{
		{1.0, 0.2, 0.2}, {1.0, 1.0, 1.0}, {0.2, 0.4, 1.0},
		{1.0, 0.2, 0.2}, {1.0, 1.0, 1.0}, {0.2, 0.4, 1.0},
		{1.0, 0.2, 0.2}, {1.0, 1.0, 1.0}, {0.2, 0.4, 1.0},
		{1.0, 0.2, 0.2}, {1.0, 1.0, 1.0}, {0.2, 0.4, 1.0},
	}
	inner := size * 0.08
	dc.SetLineWidth(size * 0.03)
	for i := 0; i < 12; i++ {
		a := float64(i) * math.Pi / 6
		// Alternate long and short rays.
		outer := size * 0.38
		if i%2 == 1 {
			outer = size * 0.25
		}
		c := colors[i]
		dc.SetRGB(c[0], c[1], c[2])
		dc.DrawLine(cx+inner*math.Cos(a), cy+inner*math.Sin(a),
			cx+outer*math.Cos(a), cy+outer*math.Sin(a))
		dc.Stroke()
		dc.DrawCircle(cx+outer*math.Cos(a), cy+outer*math.Sin(a), size*0.02)
		dc.Fill()
	}
	// Sparkles between the rays.
	dc.SetRGB(1.0, 1.0, 0.8)
	sparkleR := size * 0.3
	for i := 0; i < 8; i++ {
		a := float64(i)*math.Pi/4 + math.Pi/8
		sx := cx + sparkleR*math.Cos(a)
		sy := cy + sparkleR*math.Sin(a)
		// Four-pointed star sparkle.
		sr := size * 0.025
		dc.SetLineWidth(size * 0.012)
		dc.DrawLine(sx-sr, sy, sx+sr, sy)
		dc.Stroke()
		dc.DrawLine(sx, sy-sr, sx, sy+sr)
		dc.Stroke()
	}
	dc.SetRGB(1.0, 1.0, 0.9)
	dc.DrawCircle(cx, cy, size*0.06)
	dc.Fill()
}

// drawHolidayPumpkin draws an orange pumpkin with a face (Halloween).
func drawHolidayPumpkin(dc *gg.Context, cx, cy, size float64) {
	r := size * 0.3
	dc.SetRGB(1.0, 0.55, 0.1)
	dc.DrawEllipse(cx, cy, r*1.1, r)
	dc.Fill()
	dc.SetRGB(0.9, 0.45, 0.05)
	dc.DrawEllipse(cx-r*0.5, cy, r*0.7, r*0.95)
	dc.Fill()
	dc.DrawEllipse(cx+r*0.5, cy, r*0.7, r*0.95)
	dc.Fill()
	// Stem.
	dc.SetRGB(0.3, 0.5, 0.1)
	dc.DrawRoundedRectangle(cx-size*0.02, cy-r-size*0.08, size*0.04, size*0.1, 2)
	dc.Fill()
	// Face.
	dc.SetRGB(0.15, 0.1, 0.0)
	dc.MoveTo(cx-r*0.4, cy-r*0.15)
	dc.LineTo(cx-r*0.2, cy-r*0.15)
	dc.LineTo(cx-r*0.3, cy-r*0.4)
	dc.ClosePath()
	dc.Fill()
	dc.MoveTo(cx+r*0.2, cy-r*0.15)
	dc.LineTo(cx+r*0.4, cy-r*0.15)
	dc.LineTo(cx+r*0.3, cy-r*0.4)
	dc.ClosePath()
	dc.Fill()
	dc.SetLineWidth(size * 0.025)
	dc.MoveTo(cx-r*0.5, cy+r*0.3)
	dc.LineTo(cx-r*0.3, cy+r*0.15)
	dc.LineTo(cx-r*0.1, cy+r*0.35)
	dc.LineTo(cx+r*0.1, cy+r*0.15)
	dc.LineTo(cx+r*0.3, cy+r*0.35)
	dc.LineTo(cx+r*0.5, cy+r*0.3)
	dc.Stroke()
}

// drawHolidayTree draws a Christmas tree with a star on top.
func drawHolidayTree(dc *gg.Context, cx, cy, size float64) {
	h := size * 0.35
	dc.SetRGB(0.1, 0.55, 0.2)
	for i := 0; i < 3; i++ {
		baseW := size * (0.14 + float64(i)*0.07)
		top := cy - h + float64(i)*h*0.35
		bot := top + h*0.55
		dc.MoveTo(cx, top)
		dc.LineTo(cx-baseW, bot)
		dc.LineTo(cx+baseW, bot)
		dc.ClosePath()
		dc.Fill()
	}
	dc.SetRGB(0.45, 0.25, 0.1)
	trunkW := size * 0.035
	dc.DrawRectangle(cx-trunkW, cy+h*0.4, trunkW*2, size*0.08)
	dc.Fill()
	// Star on top.
	dc.SetRGB(1.0, 1.0, 0.3)
	dc.DrawCircle(cx, cy-h+size*0.01, size*0.045)
	dc.Fill()
	// Ornaments — evenly distributed across the three tiers.
	ornaments := []struct {
		x, y    float64
		r, g, b float64
	}{
		{cx + size*0.03, cy - h*0.4, 0.9, 0.2, 0.8},  // top tier — purple
		{cx - size*0.07, cy - h*0.05, 1.0, 0.2, 0.2}, // mid tier left — red
		{cx + size*0.08, cy - h*0.1, 0.2, 0.4, 1.0},  // mid tier right — blue
		{cx - size*0.12, cy + h*0.22, 1.0, 0.8, 0.1}, // bottom tier left — gold
		{cx, cy + h*0.28, 0.3, 0.9, 0.9},             // bottom tier center — cyan
		{cx + size*0.13, cy + h*0.18, 1.0, 0.3, 0.3}, // bottom tier right — red
	}
	for _, o := range ornaments {
		dc.SetRGB(o.r, o.g, o.b)
		dc.DrawCircle(o.x, o.y, size*0.022)
		dc.Fill()
	}
}

// drawHolidayEgg draws a decorated Easter egg.
func drawHolidayEgg(dc *gg.Context, cx, cy, size float64) {
	rx := size * 0.17
	ry := size * 0.24
	dc.SetRGB(0.7, 0.85, 1.0)
	dc.DrawEllipse(cx, cy, rx, ry)
	dc.Fill()
	// Stripes clipped to the egg shape — draw as filled bands.
	stripeColors := [][3]float64{
		{1.0, 0.4, 0.5}, {0.4, 0.8, 0.4}, {0.9, 0.8, 0.2},
	}
	bandH := size * 0.03
	for i, sc := range stripeColors {
		y := cy - ry*0.4 + float64(i)*ry*0.4
		dc.SetRGB(sc[0], sc[1], sc[2])
		// Compute the egg width at this y position.
		dy := (y - cy) / ry
		if math.Abs(dy) >= 1 {
			continue
		}
		halfW := rx * math.Sqrt(1-dy*dy)
		dc.DrawRectangle(cx-halfW+1, y-bandH/2, 2*(halfW-1), bandH)
		dc.Fill()
	}
}

// drawHolidayTurkey draws a simple turkey (Thanksgiving).
func drawHolidayTurkey(dc *gg.Context, cx, cy, size float64) {
	r := size * 0.15
	featherColors := [][3]float64{
		{0.8, 0.2, 0.1}, {1.0, 0.5, 0.1}, {1.0, 0.8, 0.2},
		{0.6, 0.3, 0.1}, {0.9, 0.3, 0.2},
	}
	bodyCY := cy + r*0.3
	for i, fc := range featherColors {
		a := -math.Pi/2 + float64(i-2)*math.Pi/6
		fx := cx + r*1.5*math.Cos(a)
		fy := bodyCY + r*1.5*math.Sin(a)
		dc.SetRGB(fc[0], fc[1], fc[2])
		// Rotated ellipse — long axis radiates outward from body center.
		eW := r * 0.35       // short axis (width)
		eH := r * 0.9        // long axis (height, pointing outward)
		rot := a + math.Pi/2 // rotate so long axis points radially outward
		dc.NewSubPath()
		for j := 0; j <= 32; j++ {
			t := 2 * math.Pi * float64(j) / 32
			lx := eW * math.Cos(t)
			ly := eH * math.Sin(t)
			px := fx + lx*math.Cos(rot) - ly*math.Sin(rot)
			py := fy + lx*math.Sin(rot) + ly*math.Cos(rot)
			if j == 0 {
				dc.MoveTo(px, py)
			} else {
				dc.LineTo(px, py)
			}
		}
		dc.ClosePath()
		dc.Fill()
	}
	dc.SetRGB(0.5, 0.3, 0.15)
	dc.DrawCircle(cx, cy+r*0.3, r)
	dc.Fill()
	dc.DrawCircle(cx, cy-r*0.5, r*0.5)
	dc.Fill()
	dc.SetRGB(1.0, 0.7, 0.2)
	dc.MoveTo(cx+r*0.5, cy-r*0.5)
	dc.LineTo(cx+r*0.8, cy-r*0.35)
	dc.LineTo(cx+r*0.5, cy-r*0.3)
	dc.ClosePath()
	dc.Fill()
	dc.SetRGB(0.9, 0.2, 0.15)
	dc.DrawCircle(cx+r*0.15, cy-r*0.2, r*0.15)
	dc.Fill()
	// Legs and feet.
	dc.SetRGB(1.0, 0.7, 0.2)
	dc.SetLineWidth(size * 0.02)
	legTop := cy + r*1.1
	legBot := cy + r*1.8
	// Left leg.
	dc.DrawLine(cx-r*0.3, legTop, cx-r*0.3, legBot)
	dc.Stroke()
	// Left foot — three toes.
	dc.DrawLine(cx-r*0.3, legBot, cx-r*0.55, legBot+r*0.2)
	dc.Stroke()
	dc.DrawLine(cx-r*0.3, legBot, cx-r*0.3, legBot+r*0.25)
	dc.Stroke()
	dc.DrawLine(cx-r*0.3, legBot, cx-r*0.1, legBot+r*0.2)
	dc.Stroke()
	// Right leg.
	dc.DrawLine(cx+r*0.3, legTop, cx+r*0.3, legBot)
	dc.Stroke()
	// Right foot.
	dc.DrawLine(cx+r*0.3, legBot, cx+r*0.05, legBot+r*0.2)
	dc.Stroke()
	dc.DrawLine(cx+r*0.3, legBot, cx+r*0.3, legBot+r*0.25)
	dc.Stroke()
	dc.DrawLine(cx+r*0.3, legBot, cx+r*0.5, legBot+r*0.2)
	dc.Stroke()
}

// drawHolidayDove draws a white dove outline (Martin Luther King Jr. Day).
func drawHolidayDove(dc *gg.Context, cx, cy, size float64) {
	s := size * 0.35
	dc.SetRGB(0.95, 0.95, 1.0)
	dc.SetLineWidth(size * 0.02)

	// Dove silhouette as a single continuous path — body, head, beak,
	// back to body, tail, wing sweep.
	// Body curve — start at tail, go along belly to chest.
	dc.NewSubPath()
	dc.MoveTo(cx-s*0.8, cy+s*0.1)                                           // tail tip
	dc.CubicTo(cx-s*0.3, cy+s*0.15, cx+s*0.1, cy+s*0.4, cx+s*0.4, cy+s*0.2) // belly
	dc.CubicTo(cx+s*0.6, cy+s*0.1, cx+s*0.7, cy-s*0.1, cx+s*0.55, cy-s*0.3) // chest to head
	// Head bump.
	dc.CubicTo(cx+s*0.45, cy-s*0.5, cx+s*0.2, cy-s*0.45, cx+s*0.15, cy-s*0.3) // head
	// Back line to tail.
	dc.CubicTo(cx-s*0.1, cy-s*0.15, cx-s*0.5, cy-s*0.1, cx-s*0.8, cy+s*0.1) // back
	dc.ClosePath()
	dc.FillPreserve()
	dc.SetRGB(0.85, 0.85, 0.9)
	dc.Stroke()

	// Wing line across the body.
	dc.SetRGB(0.82, 0.82, 0.88)
	dc.SetLineWidth(size * 0.015)
	dc.NewSubPath()
	dc.MoveTo(cx+s*0.2, cy+s*0.05)
	dc.CubicTo(cx, cy-s*0.15, cx-s*0.3, cy-s*0.1, cx-s*0.6, cy+s*0.05)
	dc.Stroke()

	// Eye.
	dc.SetRGB(0.2, 0.2, 0.3)
	dc.DrawCircle(cx+s*0.4, cy-s*0.3, s*0.04)
	dc.Fill()

	// Beak.
	dc.SetRGB(1.0, 0.7, 0.2)
	dc.SetLineWidth(size * 0.012)
	dc.MoveTo(cx+s*0.55, cy-s*0.22)
	dc.LineTo(cx+s*0.75, cy-s*0.18)
	dc.LineTo(cx+s*0.55, cy-s*0.14)
	dc.Stroke()

	// Olive branch in beak.
	dc.SetRGB(0.2, 0.6, 0.2)
	dc.SetLineWidth(size * 0.01)
	dc.MoveTo(cx+s*0.65, cy-s*0.15)
	dc.CubicTo(cx+s*0.5, cy+s*0.1, cx+s*0.2, cy+s*0.15, cx, cy+s*0.1)
	dc.Stroke()
	// Leaves.
	for i := 0; i < 3; i++ {
		t := 0.3 + float64(i)*0.25
		bx := cx + s*(0.65-t*0.65)
		by := cy + s*(-0.15+t*0.25)
		dc.DrawCircle(bx, by-s*0.04, s*0.035)
		dc.Fill()
	}
}

// drawHolidayShield draws a red/white/blue shield (Presidents' Day).
func drawHolidayShield(dc *gg.Context, cx, cy, size float64) {
	s := size * 0.35
	// Shield outline path — flat top, pointed bottom.
	dc.NewSubPath()
	dc.MoveTo(cx-s*0.7, cy-s*0.7)
	dc.LineTo(cx+s*0.7, cy-s*0.7)
	dc.LineTo(cx+s*0.7, cy+s*0.1)
	dc.CubicTo(cx+s*0.65, cy+s*0.6, cx+s*0.2, cy+s*0.9, cx, cy+s*1.0)
	dc.CubicTo(cx-s*0.2, cy+s*0.9, cx-s*0.65, cy+s*0.6, cx-s*0.7, cy+s*0.1)
	dc.ClosePath()

	// Fill white base.
	dc.SetRGB(1.0, 1.0, 1.0)
	dc.FillPreserve()

	// Red stripes — clip to shield shape.
	dc.Clip()
	dc.SetRGB(0.8, 0.12, 0.15)
	stripeH := s * 0.22
	for i := 0; i < 7; i += 2 {
		y := cy - s*0.7 + float64(i)*stripeH
		dc.DrawRectangle(cx-s, y, s*2, stripeH)
		dc.Fill()
	}

	// Blue canton (upper left).
	dc.SetRGB(0.15, 0.2, 0.55)
	dc.DrawRectangle(cx-s*0.7, cy-s*0.7, s*0.65, s*0.65)
	dc.Fill()

	// Stars in the canton.
	dc.SetRGB(1.0, 1.0, 1.0)
	starPositions := [][2]float64{
		{cx - s*0.55, cy - s*0.55},
		{cx - s*0.38, cy - s*0.55},
		{cx - s*0.55, cy - s*0.35},
		{cx - s*0.38, cy - s*0.35},
		{cx - s*0.47, cy - s*0.45},
	}
	for _, sp := range starPositions {
		dc.DrawCircle(sp[0], sp[1], s*0.045)
		dc.Fill()
	}
	dc.ResetClip()

	// Shield outline.
	dc.SetRGB(0.3, 0.3, 0.35)
	dc.SetLineWidth(size * 0.015)
	dc.NewSubPath()
	dc.MoveTo(cx-s*0.7, cy-s*0.7)
	dc.LineTo(cx+s*0.7, cy-s*0.7)
	dc.LineTo(cx+s*0.7, cy+s*0.1)
	dc.CubicTo(cx+s*0.65, cy+s*0.6, cx+s*0.2, cy+s*0.9, cx, cy+s*1.0)
	dc.CubicTo(cx-s*0.2, cy+s*0.9, cx-s*0.65, cy+s*0.6, cx-s*0.7, cy+s*0.1)
	dc.ClosePath()
	dc.Stroke()
}

// drawHolidayPoppy draws a red poppy flower (Memorial Day).
func drawHolidayPoppy(dc *gg.Context, cx, cy, size float64) {
	r := size * 0.14
	// Stem drawn first so petals cover it.
	dc.SetRGB(0.15, 0.5, 0.15)
	dc.SetLineWidth(size * 0.025)
	dc.MoveTo(cx, cy+r*1.0)
	dc.CubicTo(cx-r*0.3, cy+r*2.0, cx+r*0.2, cy+r*2.8, cx-r*0.1, cy+r*3.2)
	dc.Stroke()
	// Petals — five overlapping red circles.
	dc.SetRGB(0.85, 0.1, 0.1)
	for i := 0; i < 5; i++ {
		a := float64(i) * 2 * math.Pi / 5
		px := cx + r*0.6*math.Cos(a)
		py := cy + r*0.6*math.Sin(a)
		dc.DrawCircle(px, py, r)
		dc.Fill()
	}
	// Dark center.
	dc.SetRGB(0.15, 0.1, 0.1)
	dc.DrawCircle(cx, cy, r*0.4)
	dc.Fill()
}

// drawHolidayJuneteenth draws the Juneteenth flag — blue background, red arc,
// white star burst, and a 5-pointed star in the center.
func drawHolidayJuneteenth(dc *gg.Context, cx, cy, size float64) {
	s := size * 0.35

	// Blue background — rounded rectangle for flag shape.
	dc.SetRGB(0.11, 0.19, 0.38) // #1C3160
	dc.DrawRoundedRectangle(cx-s*1.0, cy-s*0.67, s*2.0, s*1.34, s*0.05)
	dc.Fill()

	// Red curved bottom — arc from left to right across the lower portion.
	dc.SetRGB(0.82, 0.13, 0.16) // #D0202A
	dc.NewSubPath()
	dc.MoveTo(cx-s*1.0, cy+s*0.67) // bottom-left
	dc.LineTo(cx+s*1.0, cy+s*0.67) // bottom-right
	// Arc upward across the flag.
	dc.LineTo(cx+s*1.0, cy+s*0.1)
	dc.CubicTo(cx+s*0.5, cy-s*0.15, cx-s*0.5, cy-s*0.15, cx-s*1.0, cy+s*0.1)
	dc.ClosePath()
	dc.Fill()

	// White 12-pointed star outline centered on the flag.
	dc.SetRGB(1.0, 1.0, 1.0)
	starCY := cy - s*0.05
	outerR := s * 0.5
	innerR := s * 0.25
	// Outer 12-pointed star.
	dc.NewSubPath()
	for i := 0; i < 24; i++ {
		a := float64(i)*math.Pi/12 - math.Pi/2
		r := outerR
		if i%2 == 1 {
			r = innerR
		}
		x := cx + r*math.Cos(a)
		y := starCY + r*math.Sin(a)
		if i == 0 {
			dc.MoveTo(x, y)
		} else {
			dc.LineTo(x, y)
		}
	}
	dc.ClosePath()
	// Inner cutout — smaller 12-pointed star to make it an outline.
	cutOuterR := outerR * 0.75
	cutInnerR := innerR * 0.75
	// Draw inner path in reverse to create a hole.
	for i := 23; i >= 0; i-- {
		a := float64(i)*math.Pi/12 - math.Pi/2
		r := cutOuterR
		if i%2 == 1 {
			r = cutInnerR
		}
		x := cx + r*math.Cos(a)
		y := starCY + r*math.Sin(a)
		dc.LineTo(x, y)
	}
	dc.ClosePath()
	dc.Fill()

	// White 5-pointed filled star in the center.
	star5R := s * 0.15
	star5Inner := star5R * 0.4
	dc.NewSubPath()
	for i := 0; i < 10; i++ {
		a := float64(i)*math.Pi/5 - math.Pi/2
		r := star5R
		if i%2 == 1 {
			r = star5Inner
		}
		x := cx + r*math.Cos(a)
		y := starCY + r*math.Sin(a)
		if i == 0 {
			dc.MoveTo(x, y)
		} else {
			dc.LineTo(x, y)
		}
	}
	dc.ClosePath()
	dc.Fill()
}

// drawHolidayTools draws crossed hammer and wrench (Labor Day).
func drawHolidayTools(dc *gg.Context, cx, cy, size float64) {
	s := size * 0.32

	// Both tools cross at the center, each angled ~45 degrees.
	// Wrench: upper-left to lower-right.
	wAngle := math.Pi / 4 // 45 degrees
	wLen := s * 1.3
	wx1 := cx - wLen/2*math.Cos(wAngle)
	wy1 := cy - wLen/2*math.Sin(wAngle)
	wx2 := cx + wLen/2*math.Cos(wAngle)
	wy2 := cy + wLen/2*math.Sin(wAngle)

	// Wrench shaft.
	dc.SetRGB(0.6, 0.6, 0.65)
	dc.SetLineWidth(size * 0.025)
	dc.DrawLine(wx1, wy1, wx2, wy2)
	dc.Stroke()

	// Wrench head at top-left — U-shaped open jaw.
	perp := wAngle + math.Pi/2
	jawW := s * 0.16
	jawDepth := s * 0.25
	dc.SetLineWidth(size * 0.025)
	// Start at the shaft end, draw the U: right prong, semicircle, left prong.
	dc.NewSubPath()
	// Right prong.
	dc.MoveTo(wx1+jawW*math.Cos(perp), wy1+jawW*math.Sin(perp))
	tipRX := wx1 - jawDepth*math.Cos(wAngle) + jawW*math.Cos(perp)
	tipRY := wy1 - jawDepth*math.Sin(wAngle) + jawW*math.Sin(perp)
	dc.LineTo(tipRX, tipRY)
	// Semicircle connecting the prongs at the end.
	uCX := wx1 - jawDepth*math.Cos(wAngle)
	uCY := wy1 - jawDepth*math.Sin(wAngle)
	for j := 0; j <= 16; j++ {
		ua := perp - math.Pi*float64(j)/16
		dc.LineTo(uCX+jawW*math.Cos(ua), uCY+jawW*math.Sin(ua))
	}
	// Left prong back to shaft.
	dc.LineTo(wx1-jawW*math.Cos(perp), wy1-jawW*math.Sin(perp))
	dc.Stroke()

	// Wrench closed end at bottom-right — small circle.
	dc.SetRGB(0.55, 0.55, 0.6)
	endX := wx2 + s*0.05*math.Cos(wAngle)
	endY := wy2 + s*0.05*math.Sin(wAngle)
	dc.DrawCircle(endX, endY, s*0.1)
	dc.Stroke()

	// Hammer: upper-right to lower-left, crossing at center.
	hAngle := -math.Pi / 4 // -45 degrees
	hLen := s * 1.3
	hx1 := cx - hLen/2*math.Cos(hAngle) // lower-left (handle end)
	hy1 := cy - hLen/2*math.Sin(hAngle)
	hx2 := cx + hLen/2*math.Cos(hAngle) // upper-right (head end)
	hy2 := cy + hLen/2*math.Sin(hAngle)

	// Hammer handle — brown.
	dc.SetRGB(0.55, 0.35, 0.15)
	dc.SetLineWidth(size * 0.025)
	dc.DrawLine(hx1, hy1, hx2, hy2)
	dc.Stroke()

	// Hammer head — gray filled rectangle at the upper-right end.
	dc.SetRGB(0.5, 0.5, 0.55)
	hPerp := hAngle + math.Pi/2
	headCX := hx2 + s*0.05*math.Cos(hAngle)
	headCY := hy2 + s*0.05*math.Sin(hAngle)
	headL := s * 0.28
	headW := s * 0.13
	dc.NewSubPath()
	dc.MoveTo(headCX-headL*math.Cos(hPerp)-headW*math.Cos(hAngle),
		headCY-headL*math.Sin(hPerp)-headW*math.Sin(hAngle))
	dc.LineTo(headCX+headL*math.Cos(hPerp)-headW*math.Cos(hAngle),
		headCY+headL*math.Sin(hPerp)-headW*math.Sin(hAngle))
	dc.LineTo(headCX+headL*math.Cos(hPerp)+headW*math.Cos(hAngle),
		headCY+headL*math.Sin(hPerp)+headW*math.Sin(hAngle))
	dc.LineTo(headCX-headL*math.Cos(hPerp)+headW*math.Cos(hAngle),
		headCY-headL*math.Sin(hPerp)+headW*math.Sin(hAngle))
	dc.ClosePath()
	dc.Fill()
}

// drawHolidayFeather draws a decorated feather (Indigenous Peoples' Day).
func drawHolidayFeather(dc *gg.Context, cx, cy, size float64) {
	s := size * 0.35

	// Shaft — straight vertical line with a pointed tip at top.
	shaftTop := cy - s*0.9
	shaftBot := cy + s*0.9

	// Left vane — smooth curve from tip to base.
	dc.SetRGB(0.3, 0.55, 0.7)
	dc.NewSubPath()
	dc.MoveTo(cx, shaftTop)
	dc.CubicTo(cx-s*0.5, cy-s*0.4, cx-s*0.45, cy+s*0.1, cx-s*0.15, cy+s*0.4)
	dc.LineTo(cx, cy+s*0.4)
	dc.LineTo(cx, shaftTop)
	dc.ClosePath()
	dc.Fill()

	// Right vane.
	dc.SetRGB(0.35, 0.6, 0.75)
	dc.NewSubPath()
	dc.MoveTo(cx, shaftTop)
	dc.CubicTo(cx+s*0.45, cy-s*0.4, cx+s*0.4, cy+s*0.1, cx+s*0.12, cy+s*0.4)
	dc.LineTo(cx, cy+s*0.4)
	dc.LineTo(cx, shaftTop)
	dc.ClosePath()
	dc.Fill()

	// Shaft drawn on top.
	dc.SetRGB(0.85, 0.8, 0.7)
	dc.SetLineWidth(size * 0.018)
	dc.DrawLine(cx, shaftTop, cx, shaftBot)
	dc.Stroke()

	// Quill tip — tapers to a point at the bottom.
	dc.SetRGB(0.9, 0.85, 0.75)
	dc.SetLineWidth(size * 0.01)
	dc.DrawLine(cx, cy+s*0.4, cx, shaftBot)
	dc.Stroke()

	// Decorative bands where vane meets quill.
	bandY := cy + s*0.42
	dc.SetLineWidth(size * 0.022)
	dc.SetRGB(0.85, 0.2, 0.15)
	dc.DrawLine(cx-s*0.12, bandY, cx+s*0.12, bandY)
	dc.Stroke()
	dc.SetRGB(0.9, 0.75, 0.15)
	dc.DrawLine(cx-s*0.1, bandY+s*0.08, cx+s*0.1, bandY+s*0.08)
	dc.Stroke()
	dc.SetRGB(0.2, 0.55, 0.3)
	dc.DrawLine(cx-s*0.08, bandY+s*0.16, cx+s*0.08, bandY+s*0.16)
	dc.Stroke()
}

// drawHolidayMedal draws a military medal/star (Veterans Day).
func drawHolidayMedal(dc *gg.Context, cx, cy, size float64) {
	s := size * 0.3
	// Ribbon — blue with white/red stripes.
	dc.SetRGB(0.15, 0.25, 0.7)
	dc.DrawRoundedRectangle(cx-s*0.3, cy-s*0.9, s*0.6, s*0.7, 3)
	dc.Fill()
	dc.SetRGB(1.0, 1.0, 1.0)
	dc.DrawRectangle(cx-s*0.1, cy-s*0.9, s*0.04, s*0.7)
	dc.Fill()
	dc.DrawRectangle(cx+s*0.06, cy-s*0.9, s*0.04, s*0.7)
	dc.Fill()
	dc.SetRGB(0.8, 0.15, 0.1)
	dc.DrawRectangle(cx-s*0.02, cy-s*0.9, s*0.04, s*0.7)
	dc.Fill()
	// Gold star medal.
	dc.SetRGB(0.9, 0.75, 0.2)
	points := 5
	outerR := s * 0.45
	innerR := s * 0.18
	dc.NewSubPath()
	for i := 0; i < points*2; i++ {
		a := float64(i)*math.Pi/float64(points) - math.Pi/2
		r := outerR
		if i%2 == 1 {
			r = innerR
		}
		x := cx + r*math.Cos(a)
		y := cy + s*0.15 + r*math.Sin(a)
		if i == 0 {
			dc.MoveTo(x, y)
		} else {
			dc.LineTo(x, y)
		}
	}
	dc.ClosePath()
	dc.Fill()
}

// RenderHolidayIconSheet draws all holiday icons in a grid and saves to path.
func RenderHolidayIconSheet(path string) error {
	type entry struct {
		draw  func(dc *gg.Context, cx, cy, size float64)
		label string
	}
	holidays := []entry{
		{drawHolidayChampagne, "New Year's Day\nJan 1"},
		{drawHolidayDove, "MLK Day\n3rd Mon Jan"},
		{drawHolidayShield, "Presidents' Day\n3rd Mon Feb"},
		{drawHolidayHeart, "Valentine's Day\nFeb 14"},
		{drawHolidayShamrock, "St. Patrick's Day\nMar 17"},
		{drawHolidayEgg, "Easter\n(variable)"},
		{drawHolidayPoppy, "Memorial Day\nLast Mon May"},
		{drawHolidayJuneteenth, "Juneteenth\nJun 19"},
		{drawHolidayFirework, "Independence Day\nJul 4"},
		{drawHolidayTools, "Labor Day\n1st Mon Sep"},
		{drawHolidayFeather, "Indigenous Peoples'\n2nd Mon Oct"},
		{drawHolidayPumpkin, "Halloween\nOct 31"},
		{drawHolidayMedal, "Veterans Day\nNov 11"},
		{drawHolidayTurkey, "Thanksgiving\n(variable)"},
		{drawHolidayTree, "Christmas\nDec 25"},
	}

	cols := 4
	rows := (len(holidays) + cols - 1) / cols
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
	// Render each icon to a temp canvas, measure its bounding box,
	// then draw it scaled and centered into the target area.
	tmpSize := 400
	target := iconSize * 0.8 // target bounding box for normalized icons
	for i, e := range holidays {
		col := i % cols
		row := i / cols
		cx := float64(col)*cellW + cellW/2
		cy := float64(row)*cellH + cellH/2 - 30

		// Render to temp canvas to measure bounds.
		tmp := gg.NewContext(tmpSize, tmpSize)
		tc := float64(tmpSize) / 2
		e.draw(tmp, tc, tc, float64(tmpSize)*0.8)
		img := tmp.Image().(*image.RGBA)

		// Measure bounding box of non-transparent pixels.
		minX, minY, maxX, maxY := tmpSize, tmpSize, 0, 0
		for py := 0; py < tmpSize; py++ {
			for px := 0; px < tmpSize; px++ {
				if img.RGBAAt(px, py).A > 10 {
					if px < minX {
						minX = px
					}
					if py < minY {
						minY = py
					}
					if px > maxX {
						maxX = px
					}
					if py > maxY {
						maxY = py
					}
				}
			}
		}

		if maxX > minX && maxY > minY {
			bw := float64(maxX - minX)
			bh := float64(maxY - minY)
			// Scale to fit target, maintaining aspect ratio.
			scale := target / math.Max(bw, bh)
			// Center of the bounding box in tmp coords.
			bcx := float64(minX) + bw/2
			bcy := float64(minY) + bh/2
			// Offset from tmp center.
			offX := (bcx - tc) * scale
			offY := (bcy - tc) * scale
			e.draw(dc, cx-offX, cy-offY, float64(tmpSize)*0.8*scale)
		} else {
			e.draw(dc, cx, cy, iconSize)
		}

		dc.SetRGB(textR, textG, textB)
		dc.SetFontFace(defaultFonts.small)
		lines := strings.Split(e.label, "\n")
		for j, line := range lines {
			dc.DrawStringAnchored(line, cx, cy+iconSize/2+16+float64(j)*22, 0.5, 0.5)
		}
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
