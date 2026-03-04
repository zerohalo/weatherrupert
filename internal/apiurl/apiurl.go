package apiurl

import "sync"

// NWS Weather API (base URL; overridable via WEATHER_API_URL env var).
const DefaultNWSBase = "https://api.weather.gov"

// IEM (Iowa Environmental Mesonet) radar/satellite imagery.
const IEMRadar = "https://mesonet.agron.iastate.edu/GIS/radmap.php"

// NOAA CO-OPS tide data.
const (
	TideStations    = "https://api.tidesandcurrents.noaa.gov/mdapi/prod/webapi/stations.json?type=tidepredictions"
	TidePredictions = "https://api.tidesandcurrents.noaa.gov/api/prod/datagetter" // + query params
	TideHiLo        = TidePredictions                                              // same endpoint, different params
)

// NASA SDO solar images (tried in order; first success wins).
var SunspotURLs = []string{
	"https://umbra.nascom.nasa.gov/images/latest_hmi_igram.gif",
	"https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_HMIIC.jpg",
}
var CoronaURLs = []string{
	"https://umbra.nascom.nasa.gov/images/latest_aia_304.gif",
	"https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_0304.jpg",
}

// NOAA SWPC space weather JSON APIs.
const (
	NOAAScales    = "https://services.swpc.noaa.gov/products/noaa-scales.json"
	NOAAXRayFlare = "https://services.swpc.noaa.gov/json/goes/primary/xray-flares-latest.json"
	NOAAKpIndex   = "https://services.swpc.noaa.gov/json/planetary_k_index_1m.json"
	NOAASolarWind = "https://services.swpc.noaa.gov/products/solar-wind/plasma-5-minute.json"
)

// Open Trivia Database.
const OpenTriviaDB = "https://opentdb.com/api.php"

// Classifier maps request hostnames to human-readable service labels for the
// API stats dashboard. Music stream hosts can be registered dynamically so
// they appear by name rather than by hostname.
type Classifier struct {
	nwsHost string
	mu      sync.RWMutex
	streams map[string]string // hostname → stream display name
}

// NewClassifier creates a Classifier. nwsHost is the (possibly proxied) NWS
// hostname so a custom proxy URL is labelled correctly.
func NewClassifier(nwsHost string) *Classifier {
	return &Classifier{
		nwsHost: nwsHost,
		streams: make(map[string]string),
	}
}

// RegisterStream associates a stream hostname with a display name so it shows
// as "Music: Secret Agent" instead of "Music: ice1.somafm.com".
func (c *Classifier) RegisterStream(host, name string) {
	c.mu.Lock()
	c.streams[host] = name
	c.mu.Unlock()
}

// Classify maps a hostname to a service label.
func (c *Classifier) Classify(host string) string {
	switch {
	case host == c.nwsHost:
		return "NWS Weather"
	case host == "api.tidesandcurrents.noaa.gov":
		return "NOAA Tides"
	case host == "mesonet.agron.iastate.edu":
		return "IEM Radar/Satellite"
	case host == "umbra.nascom.nasa.gov" || host == "sdo.gsfc.nasa.gov":
		return "NASA SDO Solar"
	case host == "services.swpc.noaa.gov":
		return "NOAA Space Weather"
	case host == "opentdb.com":
		return "Open Trivia DB"
	default:
		c.mu.RLock()
		name := c.streams[host]
		c.mu.RUnlock()
		if name != "" {
			return "Music: " + name
		}
		return "Music: " + host
	}
}
