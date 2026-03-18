// Package plog provides a simple per-pipeline logger that bakes in the
// component name and pipeline identifier (e.g. ZIP code) so callers don't
// need to thread them through every log call.
//
// Usage:
//
//	log := plog.New("pipeline", "01462")
//	log.Printf("started (%dx%d)", 1280, 720)
//	// output: pipeline [01462]: started (1280x720)
package plog

import (
	"fmt"
	"log"
)

// Logger is a lightweight wrapper around the standard logger that prepends
// a fixed "component [id]:" prefix to every message.
type Logger struct {
	prefix string
}

// New creates a Logger that prefixes every message with "component [id]: ".
// The component name is left-padded to 8 characters so that the [id] column
// lines up across different components (e.g. ffmpeg, weather, pipeline).
func New(component, id string) *Logger {
	return &Logger{prefix: fmt.Sprintf("%-8s [%s]: ", component, id)}
}

// Printf logs a formatted message with the baked-in prefix.
func (l *Logger) Printf(format string, args ...interface{}) {
	log.Printf(l.prefix+format, args...)
}
