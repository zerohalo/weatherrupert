// Package apistats tracks HTTP response bytes and request counts per external
// API service. A counting RoundTripper wraps response bodies so all reads are
// automatically metered without per-call-site instrumentation.
package apistats

import (
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
)

// ServiceStat is a point-in-time snapshot of one service's counters.
type ServiceStat struct {
	Name     string
	Requests int64
	Bytes    int64
}

type counters struct {
	bytes    atomic.Int64
	requests atomic.Int64
}

// Tracker accumulates per-service byte and request counts.
type Tracker struct {
	classify func(string) string
	mu       sync.Mutex
	services map[string]*counters
}

// New creates a Tracker. classify maps a request hostname to a human-readable
// service label (e.g. "api.weather.gov" → "NWS Weather").
func New(classify func(string) string) *Tracker {
	return &Tracker{
		classify: classify,
		services: make(map[string]*counters),
	}
}

// Stats returns a snapshot of all services sorted by bytes descending.
func (t *Tracker) Stats() []ServiceStat {
	t.mu.Lock()
	stats := make([]ServiceStat, 0, len(t.services))
	for name, c := range t.services {
		stats = append(stats, ServiceStat{
			Name:     name,
			Requests: c.requests.Load(),
			Bytes:    c.bytes.Load(),
		})
	}
	t.mu.Unlock()

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Bytes > stats[j].Bytes
	})
	return stats
}

// counter returns (or creates) the counters for a service name.
func (t *Tracker) counter(name string) *counters {
	t.mu.Lock()
	c := t.services[name]
	if c == nil {
		c = &counters{}
		t.services[name] = c
	}
	t.mu.Unlock()
	return c
}

// Transport returns an http.RoundTripper that wraps base, counting all
// response body bytes read and requests made per service.
func (t *Tracker) Transport(base http.RoundTripper) http.RoundTripper {
	return &countingTransport{tracker: t, base: base}
}

type countingTransport struct {
	tracker *Tracker
	base    http.RoundTripper
}

func (ct *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := ct.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	name := ct.tracker.classify(req.URL.Hostname())
	c := ct.tracker.counter(name)
	c.requests.Add(1)

	if resp.Body != nil {
		resp.Body = &countingReader{rc: resp.Body, bytes: &c.bytes}
	}
	return resp, nil
}

type countingReader struct {
	rc    io.ReadCloser
	bytes *atomic.Int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.rc.Read(p)
	if n > 0 {
		cr.bytes.Add(int64(n))
	}
	return n, err
}

func (cr *countingReader) Close() error {
	return cr.rc.Close()
}
