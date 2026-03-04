package announcements

import (
	"encoding/csv"
	"log"
	"os"
	"strings"
	"time"
)

// Announcement is a single entry from announcements.csv.
// Date is an optional "MM-DD" value; if set, the announcement is only shown
// on that calendar day each year (e.g. "02-14" for Valentine's Day).
// An empty Date means the announcement is shown every day.
type Announcement struct {
	Text string
	Date string // "MM-DD" or "" for always
}

// defaults are shown when no announcements.csv is found.
var defaults = []Announcement{
	{Text: "In the beginning the Universe was created. This has made a lot of people very angry and been widely regarded as a bad move."},
}

// Load reads announcements from a CSV file at path.
// Each row must have at least one column (text). An optional second column
// holds a date in MM-DD format; rows with other values in that column are
// loaded without a date restriction.
// A header row whose first field is "text" (case-insensitive) is silently
// skipped. If the file does not exist or cannot be parsed, the built-in
// defaults are returned.
func Load(path string) []Announcement {
	anns, err := load(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("announcements: %v (using defaults)", err)
		}
		return defaults
	}
	if len(anns) == 0 {
		log.Printf("announcements: %s is empty (using defaults)", path)
		return defaults
	}
	log.Printf("announcements: loaded %d announcement(s) from %s", len(anns), path)
	return anns
}

func load(path string) ([]Announcement, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.Comment = '#'

	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}

	var anns []Announcement
	for i, row := range rows {
		if len(row) == 0 {
			continue
		}
		text := strings.TrimSpace(row[0])
		if text == "" {
			continue
		}
		// Skip an optional header row.
		if i == 0 && strings.EqualFold(text, "text") {
			continue
		}
		var date string
		if len(row) >= 2 {
			if d := strings.TrimSpace(row[1]); d != "" {
				if _, err := time.Parse("01-02", d); err == nil {
					date = d
				}
			}
		}
		anns = append(anns, Announcement{Text: text, Date: date})
	}
	return anns, nil
}
