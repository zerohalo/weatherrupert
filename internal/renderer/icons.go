package renderer

import (
	"math"
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
)

// conditionIcon maps a free-text weather description to an iconType.
// isDaytime should reflect whether the period is during daylight hours.
func conditionIcon(desc string, isDaytime bool) iconType {
	s := strings.ToLower(desc)
	switch {
	case anyOf(s, "thunder", "lightning"):
		return iconThunderstorm
	case anyOf(s, "sleet", "freezing rain", "ice pellet", "wintry mix", "mixed rain", "ice"):
		return iconSleet
	case anyOf(s, "snow", "blizzard", "flurr"):
		return iconSnow
	case anyOf(s, "rain", "shower", "drizzle"):
		return iconRain
	case anyOf(s, "fog", "mist", "haze", "smoke", "dust"):
		return iconFog
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

	dc.SetRGB(bgR, bgG, bgB)

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
	inner := size * 0.33
	outer := size * 0.47

	// Rays
	dc.SetRGB(hlR, hlG, 0)
	dc.SetLineWidth(size * 0.07)
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

// drawMoon draws a white crescent moon using circle-subtraction.
func drawMoon(dc *gg.Context, cx, cy, size float64) {
	r := size * 0.30

	// Full disc
	dc.SetRGB(0.95, 0.95, 0.80)
	dc.DrawCircle(cx, cy, r)
	dc.Fill()

	// Overlay an offset disc in the background color to carve the crescent
	dc.SetRGB(bgR, bgG, bgB)
	dc.DrawCircle(cx+r*0.45, cy-r*0.10, r*0.78)
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

	// Fill rectangle at the bottom to unify the cloud silhouette into a flat base.
	dc.DrawRectangle(cx-size*0.42, baseY-size*0.01, size*0.84, size*0.22)
	dc.Fill()
}

// drawPartlyCloudy draws a sun or moon peeking behind a cloud.
func drawPartlyCloudy(dc *gg.Context, cx, cy, size float64, night bool) {
	// Sun/moon sits upper-right, partially revealed behind the cloud.
	bx := cx + size*0.12
	by := cy - size*0.12
	br := size * 0.22

	if night {
		// Moon
		dc.SetRGB(0.95, 0.95, 0.80)
		dc.DrawCircle(bx, by, br)
		dc.Fill()
		dc.SetRGB(bgR, bgG, bgB)
		dc.DrawCircle(bx+br*0.45, by-br*0.10, br*0.78)
		dc.Fill()
	} else {
		// Rays
		dc.SetRGB(hlR, hlG, 0)
		dc.SetLineWidth(size * 0.055)
		inner := br + size*0.04
		outer := br + size*0.14
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
	sunY := cy - size*0.18
	dc.SetRGB(hlR, hlG, 0)
	dc.DrawCircle(sunX, sunY, sunR)
	dc.Fill()

	// Dominant gray cloud
	drawCloudShape(dc, cx-size*0.04, cy+size*0.04, size*0.90, 0.78, 0.80, 0.84)
}

// drawRain draws a cloud with three diagonal rain streaks below it.
func drawRain(dc *gg.Context, cx, cy, size float64) {
	cloudCY := cy - size*0.10
	drawCloudShape(dc, cx, cloudCY, size*0.80, 0.65, 0.68, 0.78)

	dc.SetRGB(divR, divG, divB)
	dc.SetLineWidth(size * 0.06)
	dropTopY := cloudCY + size*0.27
	for _, dx := range []float64{-size * 0.22, 0, size * 0.22} {
		dc.DrawLine(cx+dx-size*0.05, dropTopY, cx+dx+size*0.05, dropTopY+size*0.22)
		dc.Stroke()
	}
}

// drawThunderstorm draws a dark cloud, a yellow lightning bolt, and rain streaks.
func drawThunderstorm(dc *gg.Context, cx, cy, size float64) {
	cloudCY := cy - size*0.14
	drawCloudShape(dc, cx, cloudCY, size*0.80, 0.45, 0.45, 0.55)

	// Lightning bolt (two-segment zigzag)
	boltX := cx - size*0.02
	boltY := cloudCY + size*0.22
	dc.SetRGB(hlR, hlG, 0)
	dc.SetLineWidth(size * 0.09)
	dc.MoveTo(boltX+size*0.09, boltY)
	dc.LineTo(boltX-size*0.04, boltY+size*0.13)
	dc.LineTo(boltX+size*0.05, boltY+size*0.13)
	dc.LineTo(boltX-size*0.09, boltY+size*0.30)
	dc.Stroke()

	// Rain streaks flanking the bolt
	dc.SetRGB(divR, divG, divB)
	dc.SetLineWidth(size * 0.055)
	for _, dx := range []float64{-size * 0.24, size * 0.20} {
		dc.DrawLine(cx+dx-size*0.04, boltY, cx+dx+size*0.04, boltY+size*0.18)
		dc.Stroke()
	}
}

// drawSnow draws a cloud with three snowflake asterisks beneath it.
func drawSnow(dc *gg.Context, cx, cy, size float64) {
	cloudCY := cy - size*0.10
	drawCloudShape(dc, cx, cloudCY, size*0.80, 0.78, 0.84, 0.90)

	dc.SetRGB(0.82, 0.94, 1.0)
	dc.SetLineWidth(size * 0.055)
	flakeY := cloudCY + size*0.32
	flakeR := size * 0.10

	for _, fx := range []float64{cx - size*0.22, cx, cx + size*0.22} {
		// 3-axis asterisk (0°, 60°, 120°)
		for i := 0; i < 3; i++ {
			a := float64(i) * math.Pi / 3
			dc.DrawLine(
				fx+flakeR*math.Cos(a), flakeY+flakeR*math.Sin(a),
				fx-flakeR*math.Cos(a), flakeY-flakeR*math.Sin(a),
			)
			dc.Stroke()
		}
	}
}

// drawSleet draws a cloud with alternating rain streaks and ice pellet dots.
func drawSleet(dc *gg.Context, cx, cy, size float64) {
	cloudCY := cy - size*0.10
	drawCloudShape(dc, cx, cloudCY, size*0.80, 0.65, 0.68, 0.72)

	dropTopY := cloudCY + size*0.27

	// Left: rain streak
	dc.SetRGB(divR, divG, divB)
	dc.SetLineWidth(size * 0.06)
	dc.DrawLine(cx-size*0.22-size*0.04, dropTopY, cx-size*0.22+size*0.04, dropTopY+size*0.20)
	dc.Stroke()

	// Center: ice pellet dot
	dc.SetRGB(0.80, 0.92, 1.0)
	dc.DrawCircle(cx, dropTopY+size*0.12, size*0.075)
	dc.Fill()

	// Right: rain streak
	dc.SetRGB(divR, divG, divB)
	dc.SetLineWidth(size * 0.06)
	dc.DrawLine(cx+size*0.22-size*0.04, dropTopY, cx+size*0.22+size*0.04, dropTopY+size*0.20)
	dc.Stroke()
}

// drawFog draws four rounded horizontal bars of decreasing opacity.
func drawFog(dc *gg.Context, cx, cy, size float64) {
	barH := size * 0.10
	barW := size * 0.82
	spacing := size * 0.20
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

	type swoosh struct {
		sx, sy, cp1x, cp1y, cp2x, cp2y, ex, ey float64
	}

	lines := []swoosh{
		// Top — long, sweeping right
		{
			cx - size*0.44, cy - size*0.20,
			cx - size*0.10, cy - size*0.38,
			cx + size*0.18, cy - size*0.10,
			cx + size*0.44, cy - size*0.22,
		},
		// Middle — shorter
		{
			cx - size*0.44, cy + size*0.02,
			cx - size*0.12, cy - size*0.16,
			cx + size*0.12, cy + size*0.06,
			cx + size*0.30, cy + size*0.00,
		},
		// Bottom — shortest
		{
			cx - size*0.44, cy + size*0.22,
			cx - size*0.18, cy + size*0.10,
			cx + size*0.06, cy + size*0.28,
			cx + size*0.22, cy + size*0.22,
		},
	}

	for i, l := range lines {
		alpha := 1.0 - float64(i)*0.20
		dc.SetRGBA(0.78, 0.90, 1.0, alpha)
		dc.MoveTo(l.sx, l.sy)
		dc.CubicTo(l.cp1x, l.cp1y, l.cp2x, l.cp2y, l.ex, l.ey)
		dc.Stroke()
	}
}
