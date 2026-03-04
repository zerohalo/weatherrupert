package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ann "github.com/zerohalo/weatherrupert/internal/announcements"
	"github.com/zerohalo/weatherrupert/internal/trivia"
)

func tmpPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "settings.json")
}

func TestNewStoreDefaults(t *testing.T) {
	s := NewStore(tmpPath(t), nil, nil, 8*time.Second, 10*time.Second, 20*time.Second, 2, 3, nil, false, 50, 9, "", 24*time.Hour, false)

	if s.SlideDuration() != 8*time.Second {
		t.Errorf("SlideDuration = %v, want 8s", s.SlideDuration())
	}
	if s.AnnouncementDuration() != 10*time.Second {
		t.Errorf("AnnouncementDuration = %v, want 10s", s.AnnouncementDuration())
	}
	if s.TriviaDuration() != 20*time.Second {
		t.Errorf("TriviaDuration = %v, want 20s", s.TriviaDuration())
	}
	if s.AnnouncementInterval() != 2 {
		t.Errorf("AnnouncementInterval = %d, want 2", s.AnnouncementInterval())
	}
	if s.TriviaInterval() != 3 {
		t.Errorf("TriviaInterval = %d, want 3", s.TriviaInterval())
	}
	if s.ClockFormat() != "24" {
		t.Errorf("ClockFormat = %q, want %q", s.ClockFormat(), "24")
	}
	if s.UnitSystem() != "imperial" {
		t.Errorf("UnitSystem = %q, want %q", s.UnitSystem(), "imperial")
	}
	if s.TriviaRandomize() != true {
		t.Error("TriviaRandomize should default to true")
	}
}

func TestNewStoreWithData(t *testing.T) {
	anns := []ann.Announcement{{Text: "Hello"}, {Text: "World", Date: "12-25"}}
	items := []trivia.TriviaItem{{Question: "Q?", Answer: "A"}}
	streams := []StreamEntry{{Name: "Jazz", URL: "http://jazz.fm"}}

	s := NewStore(tmpPath(t), anns, items, 5*time.Second, 8*time.Second, 15*time.Second, 1, 1, streams, false, 50, 9, "", 24*time.Hour, false)
	s.triviaBuiltin = true // enable so TriviaItems() returns builtin items

	if got := s.Announcements(); len(got) != 2 {
		t.Errorf("Announcements count = %d, want 2", len(got))
	}
	if got := s.TriviaItems(); len(got) != 1 {
		t.Errorf("TriviaItems count = %d, want 1", len(got))
	}
	if got := s.Streams(); len(got) != 1 || got[0].URL != "http://jazz.fm" {
		t.Errorf("Streams = %v, want [{Jazz http://jazz.fm}]", got)
	}
	if got := s.StreamURLs(); len(got) != 1 || got[0] != "http://jazz.fm" {
		t.Errorf("StreamURLs = %v, want [http://jazz.fm]", got)
	}
}

func TestSnapshotsAreCopies(t *testing.T) {
	anns := []ann.Announcement{{Text: "Original"}}
	s := NewStore(tmpPath(t), anns, nil, time.Second, time.Second, time.Second, 0, 0, nil, false, 50, 9, "", 24*time.Hour, false)

	// Mutating the returned slice should not affect the store.
	got := s.Announcements()
	got[0].Text = "Mutated"
	if s.Announcements()[0].Text != "Original" {
		t.Error("Announcements() should return a copy, not a reference")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := tmpPath(t)

	anns := []ann.Announcement{{Text: "Test Ann", Date: "07-04"}}
	items := []trivia.TriviaItem{{Question: "Q?", Answer: "A!"}}
	streams := []StreamEntry{{Name: "Lounge", URL: "http://lounge.fm"}}

	s1 := NewStore(path, anns, items, 10*time.Second, 12*time.Second, 25*time.Second, 3, 4, streams, false, 50, 9, "", 24*time.Hour, false)
	s1.triviaBuiltin = true
	if err := s1.saveToDisk(); err != nil {
		t.Fatalf("saveToDisk: %v", err)
	}

	// Create a new store from the same path — it should load persisted values.
	s2 := NewStore(path, nil, nil, time.Second, time.Second, time.Second, 0, 0, nil, false, 50, 9, "", 24*time.Hour, false)

	if s2.SlideDuration() != 10*time.Second {
		t.Errorf("loaded SlideDuration = %v, want 10s", s2.SlideDuration())
	}
	if s2.AnnouncementDuration() != 12*time.Second {
		t.Errorf("loaded AnnouncementDuration = %v, want 12s", s2.AnnouncementDuration())
	}
	if s2.TriviaDuration() != 25*time.Second {
		t.Errorf("loaded TriviaDuration = %v, want 25s", s2.TriviaDuration())
	}
	if s2.AnnouncementInterval() != 3 {
		t.Errorf("loaded AnnouncementInterval = %d, want 3", s2.AnnouncementInterval())
	}
	if s2.TriviaInterval() != 4 {
		t.Errorf("loaded TriviaInterval = %d, want 4", s2.TriviaInterval())
	}
	if got := s2.Announcements(); len(got) != 1 || got[0].Text != "Test Ann" || got[0].Date != "07-04" {
		t.Errorf("loaded Announcements = %v", got)
	}
	if got := s2.TriviaItems(); len(got) != 1 || got[0].Question != "Q?" {
		t.Errorf("loaded TriviaItems = %v", got)
	}
	if got := s2.Streams(); len(got) != 1 || got[0].Name != "Lounge" {
		t.Errorf("loaded Streams = %v", got)
	}
}

func TestLoadLegacyStringAnnouncements(t *testing.T) {
	path := tmpPath(t)

	// Write a legacy format where announcements is a plain string array.
	legacy := map[string]any{
		"slideDuration":        "8s",
		"announcementDuration": "10s",
		"announcementInterval": 2,
		"triviaDuration":       "20s",
		"triviaInterval":       3,
		"triviaRandomize":      true,
		"announcements":        []string{"Legacy Ann 1", "Legacy Ann 2"},
		"trivia":               []map[string]string{},
	}
	data, _ := json.MarshalIndent(legacy, "", "  ")
	os.WriteFile(path, data, 0644)

	s := NewStore(path, nil, nil, time.Second, time.Second, time.Second, 0, 0, nil, false, 50, 9, "", 24*time.Hour, false)
	got := s.Announcements()
	if len(got) != 2 {
		t.Fatalf("expected 2 legacy announcements, got %d", len(got))
	}
	if got[0].Text != "Legacy Ann 1" {
		t.Errorf("announcement[0].Text = %q, want %q", got[0].Text, "Legacy Ann 1")
	}
}

func TestLoadLegacyStreamURLs(t *testing.T) {
	path := tmpPath(t)

	legacy := map[string]any{
		"slideDuration":        "8s",
		"announcementDuration": "10s",
		"announcementInterval": 2,
		"triviaDuration":       "20s",
		"triviaInterval":       3,
		"triviaRandomize":      true,
		"streamURLs":           []string{"http://old-stream.fm", "http://old-stream2.fm"},
		"announcements":        []map[string]string{},
		"trivia":               []map[string]string{},
	}
	data, _ := json.MarshalIndent(legacy, "", "  ")
	os.WriteFile(path, data, 0644)

	s := NewStore(path, nil, nil, time.Second, time.Second, time.Second, 0, 0, nil, false, 50, 9, "", 24*time.Hour, false)
	got := s.Streams()
	if len(got) != 2 {
		t.Fatalf("expected 2 migrated streams, got %d", len(got))
	}
	if got[0].URL != "http://old-stream.fm" || got[0].Name != "" {
		t.Errorf("stream[0] = %+v, want URL=http://old-stream.fm Name=empty", got[0])
	}
}

func TestLoadNonexistentPath(t *testing.T) {
	s := NewStore("/nonexistent/path/settings.json", nil, nil, 5*time.Second, 5*time.Second, 5*time.Second, 1, 1, nil, false, 50, 9, "", 24*time.Hour, false)
	// Should use defaults without error.
	if s.SlideDuration() != 5*time.Second {
		t.Errorf("SlideDuration = %v, want 5s", s.SlideDuration())
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := tmpPath(t)
	os.WriteFile(path, []byte("{invalid json"), 0644)

	s := NewStore(path, nil, nil, 7*time.Second, 7*time.Second, 7*time.Second, 1, 1, nil, false, 50, 9, "", 24*time.Hour, false)
	if s.SlideDuration() != 7*time.Second {
		t.Errorf("SlideDuration = %v, want 7s (default)", s.SlideDuration())
	}
}

func TestLoadClockAndUnitSettings(t *testing.T) {
	path := tmpPath(t)

	data := map[string]any{
		"slideDuration":        "8s",
		"announcementDuration": "10s",
		"announcementInterval": 0,
		"triviaDuration":       "20s",
		"triviaInterval":       0,
		"triviaRandomize":      false,
		"clockFormat":          "12",
		"unitSystem":           "metric",
		"trivia":               []map[string]string{},
	}
	raw, _ := json.MarshalIndent(data, "", "  ")
	os.WriteFile(path, raw, 0644)

	s := NewStore(path, nil, nil, time.Second, time.Second, time.Second, 0, 0, nil, false, 50, 9, "", 24*time.Hour, false)
	if s.ClockFormat() != "12" {
		t.Errorf("ClockFormat = %q, want %q", s.ClockFormat(), "12")
	}
	if s.UnitSystem() != "metric" {
		t.Errorf("UnitSystem = %q, want %q", s.UnitSystem(), "metric")
	}
	if s.TriviaRandomize() != false {
		t.Error("TriviaRandomize should be false")
	}
}

func TestStreamEntryDisplayName(t *testing.T) {
	tests := []struct {
		entry StreamEntry
		want  string
	}{
		{StreamEntry{Name: "Jazz FM", URL: "http://jazz.fm"}, "Jazz FM"},
		{StreamEntry{URL: "http://stream.url"}, "http://stream.url"},
		{StreamEntry{Name: "", URL: "http://fallback"}, "http://fallback"},
	}
	for _, tt := range tests {
		if got := tt.entry.DisplayName(); got != tt.want {
			t.Errorf("DisplayName(%+v) = %q, want %q", tt.entry, got, tt.want)
		}
	}
}

func TestStreamURLsFiltersEmpty(t *testing.T) {
	streams := []StreamEntry{
		{URL: "http://a.fm"},
		{URL: ""},
		{URL: "http://b.fm"},
	}
	s := NewStore(tmpPath(t), nil, nil, time.Second, time.Second, time.Second, 0, 0, streams, false, 50, 9, "", 24*time.Hour, false)
	got := s.StreamURLs()
	if len(got) != 2 {
		t.Fatalf("StreamURLs count = %d, want 2", len(got))
	}
}

func TestFmtDurationHM(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "<1m"},
		{30 * time.Second, "<1m"},
		{5 * time.Minute, "5m"},
		{2*time.Hour + 15*time.Minute, "2h 15m"},
		{time.Hour, "1h 0m"},
	}
	for _, tt := range tests {
		if got := fmtDurationHM(tt.d); got != tt.want {
			t.Errorf("fmtDurationHM(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestFmtDurationHMS(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{30 * time.Second, "30s"},
		{5*time.Minute + 30*time.Second, "5m 30s"},
		{2*time.Hour + 15*time.Minute + 45*time.Second, "2h 15m 45s"},
		{time.Hour, "1h 0m 0s"},
	}
	for _, tt := range tests {
		if got := fmtDurationHMS(tt.d); got != tt.want {
			t.Errorf("fmtDurationHMS(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// TestSaveToDiskRace exercises concurrent saveToDisk and settings mutation
// to verify that pointer aliasing is fixed (values are copied before RUnlock).
// Run with -race.
func TestSaveToDiskRace(t *testing.T) {
	path := tmpPath(t)
	s := NewStore(path, nil, nil, time.Second, time.Second, time.Second, 0, 0, nil, true, 25, 9, "", 24*time.Hour, false)

	var wg sync.WaitGroup
	// Goroutine 1: save to disk repeatedly.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.saveToDisk()
		}
	}()
	// Goroutine 2: mutate settings fields concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.mu.Lock()
			s.triviaAPI = !s.triviaAPI
			s.triviaAPIAmount = i
			s.triviaAPICategory = i % 32
			s.planetLiveAlways = !s.planetLiveAlways
			s.mu.Unlock()
		}
	}()
	wg.Wait()

	// Verify the saved file is valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	var pd persistedData
	if err := json.Unmarshal(data, &pd); err != nil {
		t.Fatalf("saved file contains invalid JSON: %v", err)
	}
}

func TestHtmlEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{`<script>alert("xss")</script>`, `&lt;script&gt;alert(&#34;xss&#34;)&lt;/script&gt;`},
		{"a & b", "a &amp; b"},
	}
	for _, tt := range tests {
		if got := htmlEscape(tt.input); got != tt.want {
			t.Errorf("htmlEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
