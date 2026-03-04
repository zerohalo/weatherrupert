// Command genzips downloads the GeoNames US postal-code dataset and
// generates internal/geo/data/uszips.csv with columns: zip,lat,lon,city,state.
//
// Run once from the repo root:
//
//	go run ./cmd/genzips
package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	geonamesURL = "https://download.geonames.org/export/zip/US.zip"
	outPath     = "internal/geo/data/uszips.csv"
)

func main() {
	fmt.Println("Downloading", geonamesURL, "...")
	resp, err := http.Get(geonamesURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "download failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "download failed: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read body: %v\n", err)
		os.Exit(1)
	}

	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "open zip: %v\n", err)
		os.Exit(1)
	}

	var usData []byte
	for _, f := range zr.File {
		if f.Name == "US.txt" {
			rc, err := f.Open()
			if err != nil {
				fmt.Fprintf(os.Stderr, "open US.txt: %v\n", err)
				os.Exit(1)
			}
			usData, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				fmt.Fprintf(os.Stderr, "read US.txt: %v\n", err)
				os.Exit(1)
			}
			break
		}
	}
	if usData == nil {
		fmt.Fprintln(os.Stderr, "US.txt not found in archive")
		os.Exit(1)
	}

	// GeoNames US.txt is tab-separated with columns:
	// 0: country code, 1: postal code, 2: place name,
	// 3: admin name1 (state), 4: admin code1 (state abbr),
	// 5: admin name2, 6: admin code2, 7: admin name3, 8: admin code3,
	// 9: latitude, 10: longitude, 11: accuracy
	type entry struct {
		zip, lat, lon, city, state string
	}

	seen := make(map[string]bool)
	var entries []entry

	r := csv.NewReader(bytes.NewReader(usData))
	r.Comma = '\t'
	r.LazyQuotes = true
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if len(record) < 11 {
			continue
		}

		zipCode := strings.TrimSpace(record[1])
		// Pad to 5 digits
		for len(zipCode) < 5 {
			zipCode = "0" + zipCode
		}

		// Skip duplicates (keep first occurrence)
		if seen[zipCode] {
			continue
		}
		seen[zipCode] = true

		entries = append(entries, entry{
			zip:   zipCode,
			lat:   strings.TrimSpace(record[9]),
			lon:   strings.TrimSpace(record[10]),
			city:  strings.TrimSpace(record[2]),
			state: strings.TrimSpace(record[4]),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].zip < entries[j].zip
	})

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}

	f, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", outPath, err)
		os.Exit(1)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	for _, e := range entries {
		if err := w.Write([]string{e.zip, e.lat, e.lon, e.city, e.state}); err != nil {
			fmt.Fprintf(os.Stderr, "write: %v\n", err)
			os.Exit(1)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		fmt.Fprintf(os.Stderr, "flush: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Wrote %d ZIP codes to %s\n", len(entries), outPath)
}
