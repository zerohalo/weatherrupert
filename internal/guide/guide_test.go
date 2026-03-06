package guide

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestCityOnly(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Denver, CO", "Denver"},
		{"New York, NY", "New York"},
		{"Los Angeles, CA", "Los Angeles"},
		{"Portland", "Portland"},
		{"", ""},
		{", ST", ""},
	}

	for _, tt := range tests {
		got := cityOnly(tt.input)
		if got != tt.want {
			t.Errorf("cityOnly(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestM3U(t *testing.T) {
	result := M3U("42", "weather-90210", "Denver, CO", "http://host:9798/stream?zip=90210", "http://host:9798", "90210", "12h", "imperial")

	if !strings.HasPrefix(result, "#EXTM3U\n") {
		t.Error("M3U should start with #EXTM3U header")
	}
	if !strings.Contains(result, `channel-id="weather-90210"`) {
		t.Error("M3U should contain channel-id")
	}
	if !strings.Contains(result, `channel-number="42"`) {
		t.Error("M3U should contain channel-number")
	}
	if !strings.Contains(result, `tvg-name="Local Weather"`) {
		t.Error("M3U should contain tvg-name")
	}
	if !strings.Contains(result, "http://host:9798/stream?zip=90210") {
		t.Error("M3U should contain stream URL")
	}
	if !strings.HasSuffix(result, "\n") {
		t.Error("M3U should end with newline")
	}
}

func TestXMLTVStructure(t *testing.T) {
	data, err := XMLTV("weather-90210", "Denver, CO", "90210", "America/Denver")
	if err != nil {
		t.Fatalf("XMLTV() error: %v", err)
	}

	content := string(data)

	if !strings.HasPrefix(content, xml.Header) {
		t.Error("should start with XML header")
	}
	if !strings.Contains(content, `<!DOCTYPE tv SYSTEM "xmltv.dtd">`) {
		t.Error("should contain DOCTYPE declaration")
	}
}

func TestXMLTVParsesAsValidXML(t *testing.T) {
	data, err := XMLTV("weather-90210", "Denver, CO", "90210", "America/Denver")
	if err != nil {
		t.Fatalf("XMLTV() error: %v", err)
	}

	var tv xmlTV
	// Strip the DOCTYPE line since xml.Unmarshal doesn't handle it.
	cleaned := strings.Replace(string(data), `<!DOCTYPE tv SYSTEM "xmltv.dtd">`, "", 1)
	if err := xml.Unmarshal([]byte(cleaned), &tv); err != nil {
		t.Fatalf("XMLTV output is not valid XML: %v", err)
	}

	if tv.SourceInfoName != "weatherrupert" {
		t.Errorf("SourceInfoName = %q, want %q", tv.SourceInfoName, "weatherrupert")
	}
	if len(tv.Channels) != 1 {
		t.Fatalf("got %d channels, want 1", len(tv.Channels))
	}
	if tv.Channels[0].ID != "weather-90210" {
		t.Errorf("Channel ID = %q, want %q", tv.Channels[0].ID, "weather-90210")
	}
	if tv.Channels[0].DisplayName != "Local Weather" {
		t.Errorf("Channel DisplayName = %q, want %q", tv.Channels[0].DisplayName, "Local Weather")
	}
}

func TestXMLTV24Programmes(t *testing.T) {
	data, err := XMLTV("weather-90210", "Denver, CO", "90210", "America/Denver")
	if err != nil {
		t.Fatalf("XMLTV() error: %v", err)
	}

	var tv xmlTV
	cleaned := strings.Replace(string(data), `<!DOCTYPE tv SYSTEM "xmltv.dtd">`, "", 1)
	if err := xml.Unmarshal([]byte(cleaned), &tv); err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(tv.Programmes) != 24 {
		t.Fatalf("got %d programmes, want 24", len(tv.Programmes))
	}

	for i, p := range tv.Programmes {
		if p.Channel != "weather-90210" {
			t.Errorf("programme[%d].Channel = %q, want %q", i, p.Channel, "weather-90210")
		}
		if p.Title.Value != "Denver Local Weather" {
			t.Errorf("programme[%d].Title = %q, want %q", i, p.Title.Value, "Denver Local Weather")
		}
		if p.Title.Lang != "en" {
			t.Errorf("programme[%d].Title.Lang = %q, want %q", i, p.Title.Lang, "en")
		}
		if p.Category.Value != "Weather" {
			t.Errorf("programme[%d].Category = %q, want %q", i, p.Category.Value, "Weather")
		}
		if p.Start == "" || p.Stop == "" {
			t.Errorf("programme[%d] has empty start/stop times", i)
		}
	}
}

func TestXMLTVProgrammeDescription(t *testing.T) {
	data, err := XMLTV("weather-10001", "New York, NY", "10001", "America/New_York")
	if err != nil {
		t.Fatalf("XMLTV() error: %v", err)
	}

	var tv xmlTV
	cleaned := strings.Replace(string(data), `<!DOCTYPE tv SYSTEM "xmltv.dtd">`, "", 1)
	if err := xml.Unmarshal([]byte(cleaned), &tv); err != nil {
		t.Fatalf("parse error: %v", err)
	}

	if len(tv.Programmes) == 0 {
		t.Fatal("no programmes")
	}

	desc := tv.Programmes[0].Desc.Value
	if !strings.Contains(desc, "New York") {
		t.Errorf("description should contain city name, got %q", desc)
	}
	if !strings.Contains(desc, "10001") {
		t.Errorf("description should contain ZIP code, got %q", desc)
	}
}

func TestXMLTVProgrammeTimesContiguous(t *testing.T) {
	data, err := XMLTV("weather-90210", "Denver, CO", "90210", "America/Denver")
	if err != nil {
		t.Fatalf("XMLTV() error: %v", err)
	}

	var tv xmlTV
	cleaned := strings.Replace(string(data), `<!DOCTYPE tv SYSTEM "xmltv.dtd">`, "", 1)
	if err := xml.Unmarshal([]byte(cleaned), &tv); err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Verify each programme's stop time equals the next programme's start time.
	for i := 0; i < len(tv.Programmes)-1; i++ {
		if tv.Programmes[i].Stop != tv.Programmes[i+1].Start {
			t.Errorf("gap between programme[%d].Stop=%q and programme[%d].Start=%q",
				i, tv.Programmes[i].Stop, i+1, tv.Programmes[i+1].Start)
		}
	}
}
