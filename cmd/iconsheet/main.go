// Command iconsheet renders all weather condition icons in a grid and saves a PNG.
package main

import (
	"fmt"
	"os"

	"github.com/zerohalo/weatherrupert/internal/renderer"
)

func main() {
	path := "screenshots/icon-reference.png"
	if err := renderer.RenderIconSheet(path); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	realisticPath := "screenshots/icon-reference-realistic.png"
	if err := renderer.RenderRealisticIconSheet(realisticPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	funSunPath := "screenshots/icon-reference-funsun.png"
	if err := renderer.RenderFunSunIconSheet(funSunPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	holidayPath := "screenshots/holiday-icons.png"
	if err := renderer.RenderHolidayIconSheet(holidayPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	moonPath := "screenshots/moon-phase-reference.png"
	if err := renderer.RenderMoonPhaseSheet(moonPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
