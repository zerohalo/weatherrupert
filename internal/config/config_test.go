package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Clear any env vars that could interfere.
	keys := []string{
		"WEATHER_API_URL", "FRAME_RATE", "CHANNEL_NUMBER", "PORT",
		"VIDEO_WIDTH", "VIDEO_HEIGHT", "WEATHER_REFRESH", "SLIDE_DURATION",
		"MUSIC_DIR", "MUSIC_STREAM_URL", "ANNOUNCEMENTS_PATH",
		"ANNOUNCEMENT_DURATION", "TRIVIA_PATH", "TRIVIA_DURATION",
		"ADMIN_DATA_PATH", "ANNOUNCEMENT_INTERVAL", "TRIVIA_INTERVAL",
		"HLS_SEGMENT_DURATION", "HLS_PLAYLIST_SIZE", "HLS_RING_SIZE",
		"RADAR_FRAMES", "RADAR_RADIUS",
	}
	for _, k := range keys {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"WeatherAPIURL", cfg.WeatherAPIURL, "https://api.weather.gov"},
		{"FrameRate", cfg.FrameRate, 5},
		{"ChannelNumber", cfg.ChannelNumber, "100"},
		{"Port", cfg.Port, 9798},
		{"Width", cfg.Width, 1280},
		{"Height", cfg.Height, 720},
		{"WeatherRefresh", cfg.WeatherRefresh, 20 * time.Minute},
		{"SlideDuration", cfg.SlideDuration, 8 * time.Second},
		{"MusicDir", cfg.MusicDir, "/music"},
		{"MusicStreamURL", cfg.MusicStreamURL, ""},
		{"AnnouncementsPath", cfg.AnnouncementsPath, "/announcements/announcements.csv"},
		{"AnnouncementDuration", cfg.AnnouncementDuration, 10 * time.Second},
		{"TriviaPath", cfg.TriviaPath, "/trivia/trivia.csv"},
		{"TriviaDuration", cfg.TriviaDuration, 20 * time.Second},
		{"AdminDataPath", cfg.AdminDataPath, "/data/settings.json"},
		{"AnnouncementInterval", cfg.AnnouncementInterval, 2},
		{"TriviaInterval", cfg.TriviaInterval, 3},
		{"HLSSegmentDuration", cfg.HLSSegmentDuration, 3 * time.Second},
		{"HLSPlaylistSize", cfg.HLSPlaylistSize, 3},
		{"HLSRingSize", cfg.HLSRingSize, 5},
		{"Frames", cfg.Frames, 4},
		{"Radius", cfg.Radius, 120.0},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("WEATHER_API_URL", "http://proxy:8080/api")
	t.Setenv("FRAME_RATE", "30")
	t.Setenv("CHANNEL_NUMBER", "42")
	t.Setenv("PORT", "8080")
	t.Setenv("VIDEO_WIDTH", "1920")
	t.Setenv("VIDEO_HEIGHT", "1080")
	t.Setenv("WEATHER_REFRESH", "5m")
	t.Setenv("SLIDE_DURATION", "12s")
	t.Setenv("RADAR_RADIUS", "200.5")
	t.Setenv("HLS_PLAYLIST_SIZE", "6")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.WeatherAPIURL != "http://proxy:8080/api" {
		t.Errorf("WeatherAPIURL = %q, want %q", cfg.WeatherAPIURL, "http://proxy:8080/api")
	}
	if cfg.FrameRate != 30 {
		t.Errorf("FrameRate = %d, want 30", cfg.FrameRate)
	}
	if cfg.ChannelNumber != "42" {
		t.Errorf("ChannelNumber = %q, want %q", cfg.ChannelNumber, "42")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Width != 1920 {
		t.Errorf("Width = %d, want 1920", cfg.Width)
	}
	if cfg.Height != 1080 {
		t.Errorf("Height = %d, want 1080", cfg.Height)
	}
	if cfg.WeatherRefresh != 5*time.Minute {
		t.Errorf("WeatherRefresh = %v, want 5m", cfg.WeatherRefresh)
	}
	if cfg.SlideDuration != 12*time.Second {
		t.Errorf("SlideDuration = %v, want 12s", cfg.SlideDuration)
	}
	if cfg.Radius != 200.5 {
		t.Errorf("Radius = %v, want 200.5", cfg.Radius)
	}
	if cfg.HLSPlaylistSize != 6 {
		t.Errorf("HLSPlaylistSize = %d, want 6", cfg.HLSPlaylistSize)
	}
}

func TestLoadInvalidValuesFallToDefaults(t *testing.T) {
	t.Setenv("FRAME_RATE", "notanumber")
	t.Setenv("WEATHER_REFRESH", "invalid")
	t.Setenv("RADAR_RADIUS", "xyz")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.FrameRate != 5 {
		t.Errorf("FrameRate = %d, want default 5", cfg.FrameRate)
	}
	if cfg.WeatherRefresh != 20*time.Minute {
		t.Errorf("WeatherRefresh = %v, want default 20m", cfg.WeatherRefresh)
	}
	if cfg.Radius != 120.0 {
		t.Errorf("Radius = %v, want default 120.0", cfg.Radius)
	}
}

func TestHelperFunctions(t *testing.T) {
	t.Run("envOrDefault", func(t *testing.T) {
		t.Setenv("TEST_ENV_KEY", "custom")
		if got := envOrDefault("TEST_ENV_KEY", "fallback"); got != "custom" {
			t.Errorf("got %q, want %q", got, "custom")
		}
		os.Unsetenv("TEST_ENV_KEY")
		if got := envOrDefault("TEST_ENV_KEY", "fallback"); got != "fallback" {
			t.Errorf("got %q, want %q", got, "fallback")
		}
	})

	t.Run("intOrDefault", func(t *testing.T) {
		t.Setenv("TEST_INT_KEY", "42")
		if got := intOrDefault("TEST_INT_KEY", 10); got != 42 {
			t.Errorf("got %d, want 42", got)
		}
		t.Setenv("TEST_INT_KEY", "bad")
		if got := intOrDefault("TEST_INT_KEY", 10); got != 10 {
			t.Errorf("got %d, want default 10", got)
		}
	})

	t.Run("floatOrDefault", func(t *testing.T) {
		t.Setenv("TEST_FLOAT_KEY", "3.14")
		if got := floatOrDefault("TEST_FLOAT_KEY", 1.0); got != 3.14 {
			t.Errorf("got %v, want 3.14", got)
		}
		t.Setenv("TEST_FLOAT_KEY", "bad")
		if got := floatOrDefault("TEST_FLOAT_KEY", 1.0); got != 1.0 {
			t.Errorf("got %v, want default 1.0", got)
		}
	})

	t.Run("durationOrDefault", func(t *testing.T) {
		t.Setenv("TEST_DUR_KEY", "30s")
		if got := durationOrDefault("TEST_DUR_KEY", time.Minute); got != 30*time.Second {
			t.Errorf("got %v, want 30s", got)
		}
		t.Setenv("TEST_DUR_KEY", "bad")
		if got := durationOrDefault("TEST_DUR_KEY", time.Minute); got != time.Minute {
			t.Errorf("got %v, want default 1m", got)
		}
	})
}
