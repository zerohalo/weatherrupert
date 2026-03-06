package guide

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// cityOnly strips the state abbreviation from a "City, ST" location string.
func cityOnly(location string) string {
	if i := strings.Index(location, ","); i >= 0 {
		return strings.TrimSpace(location[:i])
	}
	return location
}

// M3U returns the M3U playlist content pointing to the stream endpoint.
// baseURL is the scheme://host prefix used to build logo and preview art URLs.
// zip, clock, and units are passed through for the preview query string.
func M3U(channelNumber, channelID, location, streamURL, baseURL, zip, clock, units string) string {
	chanName := "Local Weather"
	logoURL := baseURL + "/favicon.ico"
	artURL := fmt.Sprintf("%s/preview?zip=%s&_=%d", baseURL, zip, time.Now().Unix())
	return fmt.Sprintf("#EXTM3U\n#EXTINF:-1 channel-id=%q channel-number=%q tvg-id=%q tvg-name=%q"+
		" tvg-logo=%q group-title=\"Weather\""+
		" tvc-guide-placeholders=\"3600\" tvc-guide-genres=\"News\" tvc-guide-tags=\"HDTV,Live\""+
		" tvc-guide-art=%q tvc-stream-vcodec=\"h264\" tvc-stream-acodec=\"aac\""+
		",%s\n%s\n",
		channelID, channelNumber, channelID, chanName,
		logoURL, artURL, chanName, streamURL)
}

// xmlTV is the root XMLTV element.
type xmlTV struct {
	XMLName        xml.Name       `xml:"tv"`
	SourceInfoName string         `xml:"source-info-name,attr"`
	GeneratorName  string         `xml:"generator-info-name,attr"`
	Channels       []xmlChannel   `xml:"channel"`
	Programmes     []xmlProgramme `xml:"programme"`
}

type xmlChannel struct {
	ID          string `xml:"id,attr"`
	DisplayName string `xml:"display-name"`
}

type xmlProgramme struct {
	Start    string  `xml:"start,attr"`
	Stop     string  `xml:"stop,attr"`
	Channel  string  `xml:"channel,attr"`
	Title    xmlLang `xml:"title"`
	Desc     xmlLang `xml:"desc"`
	Category xmlLang `xml:"category"`
}

type xmlLang struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

const xmltvTimeLayout = "20060102150405 -0700"

// XMLTV generates XMLTV guide data with 24 hourly programme entries for the given channel.
func XMLTV(channelID, location, zip string) ([]byte, error) {
	chanName := "Local Weather"
	city := cityOnly(location)
	progTitle := city + " Local Weather"
	now := time.Now().UTC().Truncate(time.Hour)

	var programmes []xmlProgramme
	desc := fmt.Sprintf("Current conditions and forecast for %s %s", city, zip)

	for i := 0; i < 24; i++ {
		start := now.Add(time.Duration(i) * time.Hour)
		stop := start.Add(time.Hour)
		hourDesc := fmt.Sprintf("%s (updated %s)", desc, start.Format("3 PM"))
		programmes = append(programmes, xmlProgramme{
			Start:    start.Format(xmltvTimeLayout),
			Stop:     stop.Format(xmltvTimeLayout),
			Channel:  channelID,
			Title:    xmlLang{Lang: "en", Value: progTitle},
			Desc:     xmlLang{Lang: "en", Value: hourDesc},
			Category: xmlLang{Lang: "en", Value: "Weather"},
		})
	}

	tv := xmlTV{
		SourceInfoName: "weatherrupert",
		GeneratorName:  "weatherrupert",
		Channels: []xmlChannel{
			{ID: channelID, DisplayName: chanName},
		},
		Programmes: programmes,
	}

	out, err := xml.MarshalIndent(tv, "", "  ")
	if err != nil {
		return nil, err
	}

	var sb strings.Builder
	sb.WriteString(xml.Header)
	sb.WriteString(`<!DOCTYPE tv SYSTEM "xmltv.dtd">`)
	sb.WriteByte('\n')
	sb.Write(out)
	sb.WriteByte('\n')

	return []byte(sb.String()), nil
}
