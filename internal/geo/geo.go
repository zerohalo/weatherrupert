package geo

import (
	"embed"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

//go:embed data/uszips.csv
var zipData embed.FS

// Location holds the result of a ZIP code lookup.
type Location struct {
	ZipCode string
	Lat     float64
	Lon     float64
	City    string
	State   string
}

// db is loaded once at package init.
var db map[string]Location

func init() {
	db = make(map[string]Location, 40000)

	f, err := zipData.Open("data/uszips.csv")
	if err != nil {
		panic(fmt.Sprintf("geo: failed to open embedded zip data: %v", err))
	}
	defer f.Close()

	r := csv.NewReader(f)
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if len(record) < 5 {
			continue
		}

		zip := strings.TrimSpace(record[0])
		// Pad to 5 digits
		for len(zip) < 5 {
			zip = "0" + zip
		}

		lat, err := strconv.ParseFloat(strings.TrimSpace(record[1]), 64)
		if err != nil {
			continue
		}
		lon, err := strconv.ParseFloat(strings.TrimSpace(record[2]), 64)
		if err != nil {
			continue
		}

		db[zip] = Location{
			ZipCode: zip,
			Lat:     lat,
			Lon:     lon,
			City:    strings.TrimSpace(record[3]),
			State:   strings.TrimSpace(record[4]),
		}
	}
}

// Lookup returns lat/lon for the given ZIP code.
// The ZIP code is normalized to 5 digits (zero-padded if needed).
func Lookup(zip string) (Location, error) {
	zip = strings.TrimSpace(zip)
	// Take only the first 5 characters (handle ZIP+4 format)
	if len(zip) > 5 {
		zip = zip[:5]
	}
	// Pad to 5 digits
	for len(zip) < 5 {
		zip = "0" + zip
	}

	loc, ok := db[zip]
	if !ok {
		return Location{}, fmt.Errorf("ZIP code %q not found", zip)
	}
	return loc, nil
}
