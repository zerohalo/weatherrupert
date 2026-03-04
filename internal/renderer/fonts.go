package renderer

import (
	_ "embed"
	"log"

	"git.sr.ht/~sbinet/gg"
	"golang.org/x/image/font"
)

//go:embed assets/Inconsolata-Regular.ttf
var inconsolataRegularTTF []byte

//go:embed assets/Inconsolata-Bold.ttf
var inconsolataBoldTTF []byte

// fontSet holds a complete set of font faces. Each Renderer creates its own
// fontSet so that concurrent pipelines never share mutable glyph caches.
type fontSet struct {
	small      font.Face // 22pt regular - axis ticks, header sub-text
	cardBody   font.Face // 23pt regular - condition text in extended forecast cards
	medium     font.Face // 32pt regular - hourly table values, announcements
	mediumBold font.Face // 32pt bold    - right-column table labels
	mediumXL   font.Face // 48pt regular - local conditions left-column text (wind, gusts, feels)
	xl         font.Face // 64pt regular - local conditions right-column values
	large      font.Face // 64pt bold    - hero temperature
	hero       font.Face // 80pt bold    - local conditions temperature
	title      font.Face // 33pt bold    - slide title bar
	cardTitle  font.Face // 51pt bold    - day names in extended forecast cards
}

// newFontSet creates a fresh set of font faces from the embedded TTF bytes.
func newFontSet() *fontSet {
	fs := &fontSet{}
	var err error

	fs.small, err = gg.LoadFontFaceFromBytes(inconsolataRegularTTF, 22)
	if err != nil {
		log.Fatalf("renderer: load fontSmall: %v", err)
	}

	fs.cardBody, err = gg.LoadFontFaceFromBytes(inconsolataRegularTTF, 23)
	if err != nil {
		log.Fatalf("renderer: load fontCardBody: %v", err)
	}

	fs.medium, err = gg.LoadFontFaceFromBytes(inconsolataRegularTTF, 32)
	if err != nil {
		log.Fatalf("renderer: load fontMedium: %v", err)
	}

	fs.mediumBold, err = gg.LoadFontFaceFromBytes(inconsolataBoldTTF, 32)
	if err != nil {
		log.Fatalf("renderer: load fontMediumBold: %v", err)
	}

	fs.mediumXL, err = gg.LoadFontFaceFromBytes(inconsolataRegularTTF, 48)
	if err != nil {
		log.Fatalf("renderer: load fontMediumXL: %v", err)
	}

	fs.xl, err = gg.LoadFontFaceFromBytes(inconsolataRegularTTF, 64)
	if err != nil {
		log.Fatalf("renderer: load fontXL: %v", err)
	}

	fs.large, err = gg.LoadFontFaceFromBytes(inconsolataBoldTTF, 64)
	if err != nil {
		log.Fatalf("renderer: load fontLarge: %v", err)
	}

	fs.hero, err = gg.LoadFontFaceFromBytes(inconsolataBoldTTF, 80)
	if err != nil {
		log.Fatalf("renderer: load fontHero: %v", err)
	}

	fs.title, err = gg.LoadFontFaceFromBytes(inconsolataBoldTTF, 33)
	if err != nil {
		log.Fatalf("renderer: load fontTitle: %v", err)
	}

	fs.cardTitle, err = gg.LoadFontFaceFromBytes(inconsolataBoldTTF, 51)
	if err != nil {
		log.Fatalf("renderer: load fontCardTitle: %v", err)
	}

	return fs
}

// Package-level default font set, used by circledLetter cache and the
// screenshots command (single-threaded callers).
var defaultFonts = newFontSet()
