package config

import (
	"os"
	"strconv"
	"time"

	"github.com/zerohalo/weatherrupert/internal/apiurl"
)

// Setting value constants shared across packages.
const (
	ClockFormat12h = "12"
	ClockFormat24h = "24"

	UnitsImperial = "imperial"
	UnitsMetric   = "metric"

	SatelliteIR  = "IR"
	SatelliteVIS = "VIS"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	WeatherAPIURL  string // base URL for NWS API (default: https://api.weather.gov)
	FrameRate      int
	ChannelNumber  string
	Port           int
	Width          int
	Height         int
	WeatherRefresh time.Duration
	SlideDuration  time.Duration
	MusicDir            string
	MusicStreamURL      string // HTTP/Icecast stream URL; used when MUSIC_DIR has no files
	AnnouncementsPath   string        // path to announcements.csv; defaults to /announcements/announcements.csv
	AnnouncementDuration time.Duration // how long each announcement is displayed
	TriviaPath          string        // path to trivia.csv; defaults to /trivia/trivia.csv
	TriviaDuration      time.Duration // total time for each trivia question+answer cycle
	AdminDataPath        string // path to JSON settings file; defaults to /data/settings.json
	AnnouncementInterval int    // weather cycles between announcement slides (0 = disabled)
	TriviaInterval       int    // weather cycles between trivia slides (0 = disabled)

	// HLS settings
	HLSSegmentDuration time.Duration // duration of each HLS segment (default: 3s)
	HLSPlaylistSize    int           // number of segments in the HLS playlist (default: 3)
	HLSRingSize        int           // number of segments kept in memory (default: 5)

	// Radar & satellite imagery settings
	Frames int     // number of animation frames (default: 4)
	Radius float64 // bounding box radius in miles (default: 120)

	// Video encoding
	VideoMaxRate string // VBV max bitrate (e.g. "1500k"); empty = unconstrained VBR

	TriviaAPI           bool          // fetch trivia from Open Trivia Database (default: true)
	TriviaAPIAmount     int           // number of API questions to fetch (default: 25, max 50)
	TriviaAPICategory   int           // 9 = General Knowledge; 0 = any; 9–32 = specific category ID
	TriviaAPIDifficulty string        // "" = any; "easy", "medium", "hard"
	TriviaAPIRefresh    time.Duration // how often to re-fetch from API; 0 = startup only (default: 24h)
	TriviaBuiltin       bool          // include built-in/admin trivia in the pool (default: false)
}

// Load reads environment variables and returns a Config with defaults applied.
func Load() (*Config, error) {
	cfg := &Config{
		WeatherAPIURL:  envOrDefault("WEATHER_API_URL", apiurl.DefaultNWSBase),
		FrameRate:      intOrDefault("FRAME_RATE", 5),
		ChannelNumber:  envOrDefault("CHANNEL_NUMBER", "100"),
		Port:           intOrDefault("PORT", 9798),
		Width:          intOrDefault("VIDEO_WIDTH", 1280),
		Height:         intOrDefault("VIDEO_HEIGHT", 720),
		WeatherRefresh: durationOrDefault("WEATHER_REFRESH", 20*time.Minute),
		SlideDuration:  durationOrDefault("SLIDE_DURATION", 8*time.Second),
		MusicDir:          envOrDefault("MUSIC_DIR", "/music"),
		MusicStreamURL:    envOrDefault("MUSIC_STREAM_URL", ""),
		AnnouncementsPath:    envOrDefault("ANNOUNCEMENTS_PATH", "/announcements/announcements.csv"),
		AnnouncementDuration: durationOrDefault("ANNOUNCEMENT_DURATION", 10*time.Second),
		TriviaPath:           envOrDefault("TRIVIA_PATH", "/trivia/trivia.csv"),
		TriviaDuration:       durationOrDefault("TRIVIA_DURATION", 20*time.Second),
		AdminDataPath:        envOrDefault("ADMIN_DATA_PATH", "/data/settings.json"),
		AnnouncementInterval: intOrDefault("ANNOUNCEMENT_INTERVAL", 2),
		TriviaInterval:       intOrDefault("TRIVIA_INTERVAL", 3),

		HLSSegmentDuration: durationOrDefault("HLS_SEGMENT_DURATION", 3*time.Second),
		HLSPlaylistSize:    intOrDefault("HLS_PLAYLIST_SIZE", 3),
		HLSRingSize:        intOrDefault("HLS_RING_SIZE", 5),

		Frames: intOrDefault("RADAR_FRAMES", 4),
		Radius: floatOrDefault("RADAR_RADIUS", 120.0),

		VideoMaxRate: envOrDefault("VIDEO_MAXRATE", "1500k"),

		TriviaAPI:           boolOrDefault("TRIVIA_API", true),
		TriviaAPIAmount:     intOrDefault("TRIVIA_API_AMOUNT", 25),
		TriviaAPICategory:   intOrDefault("TRIVIA_API_CATEGORY", 9),
		TriviaAPIDifficulty: envOrDefault("TRIVIA_API_DIFFICULTY", ""),
		TriviaAPIRefresh:    durationOrDefault("TRIVIA_API_REFRESH", 24*time.Hour),
		TriviaBuiltin:       boolOrDefault("TRIVIA_BUILTIN", false),
	}
	return cfg, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return def
}

func floatOrDefault(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return f
		}
	}
	return def
}

func boolOrDefault(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func durationOrDefault(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return def
}
