package stream

import (
	"context"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sync"
	"time"
)

const relayChunkSize = 4096
const relayBufLen = 256 // chunks to buffer per subscriber before dropping

// MusicRelay connects to an HTTP audio stream once and fans out the raw
// audio bytes to multiple FFmpeg processes via OS pipes.  When multiple
// pipelines use the same stream URL, they share a single MusicRelay
// instead of each opening an independent HTTP connection.
//
// The relay tracks which subscribers are "active" (have viewers).  When no
// subscriber is active, the relay disconnects from the stream to avoid
// wasting bandwidth.  It reconnects when any subscriber becomes active again.
type MusicRelay struct {
	url     string
	http    *http.Client
	mu      sync.Mutex
	clients map[*relayClient]struct{}
	started bool
	active  int           // number of subscribers with viewers
	stopCh  chan struct{} // closed to signal the fetch loop to exit
	wakeCh  chan struct{} // signalled when active transitions from 0 → >0
}

type relayClient struct {
	pr       *os.File    // read end — given to FFmpeg via ExtraFiles
	pw       *os.File    // write end — writer goroutine writes here
	ch       chan []byte // buffered channel fed by broadcast
	done     chan struct{}
	active   bool  // true when this client's pipeline has viewers
	drops    int64 // chunks dropped because channel was full
	received int64 // chunks successfully queued
}

// NewMusicRelay creates a relay for the given stream URL.
func NewMusicRelay(url string, httpClient *http.Client) *MusicRelay {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &MusicRelay{
		url:     url,
		http:    httpClient,
		clients: make(map[*relayClient]struct{}),
		wakeCh:  make(chan struct{}, 1),
	}
}

// Subscribe returns the read end of an OS pipe that receives the shared
// audio stream.  The caller must pass this file to FFmpeg via ExtraFiles
// (it will appear as fd 3, i.e. "pipe:3").  Call Unsubscribe with the
// same *os.File when done.
//
// New subscribers start inactive.  Call SetActive(pipe, true) when viewers
// connect to start receiving data.
func (r *MusicRelay) Subscribe() *os.File {
	pr, pw, err := os.Pipe()
	if err != nil {
		log.Printf("music relay: pipe error: %v", err)
		return nil
	}

	c := &relayClient{
		pr:   pr,
		pw:   pw,
		ch:   make(chan []byte, relayBufLen),
		done: make(chan struct{}),
	}

	// Writer goroutine: reads from channel, writes to OS pipe.
	// Blocks when FFmpeg is suspended (pipe buffer full) — that's fine;
	// the channel fills up and broadcast drops data for this client.
	go runRelayWriter(c)

	r.mu.Lock()
	r.clients[c] = struct{}{}
	needStart := !r.started
	if needStart {
		r.started = true
		r.stopCh = make(chan struct{})
	}
	r.mu.Unlock()

	if needStart {
		go r.run()
	}

	log.Printf("music relay: subscriber added (%d total) for %s", r.clientCount(), r.url)
	return pr
}

// Unsubscribe removes a subscriber and closes its pipe.
func (r *MusicRelay) Unsubscribe(pr *os.File) {
	r.mu.Lock()
	var found *relayClient
	for c := range r.clients {
		if c.pr == pr {
			found = c
			if c.active {
				r.active--
			}
			delete(r.clients, c)
			break
		}
	}
	shouldStop := len(r.clients) == 0 && r.started
	if shouldStop {
		r.started = false
		close(r.stopCh)
	}
	r.mu.Unlock()

	if found != nil {
		close(found.ch)
		<-found.done // wait for writer goroutine to finish
		found.pr.Close()
	}

	log.Printf("music relay: subscriber removed (%d remaining) for %s", r.clientCount(), r.url)
}

// SetActive marks a subscriber as active (has viewers) or inactive (no viewers).
// When the first subscriber becomes active, the relay connects to the stream.
// When the last active subscriber becomes inactive, the relay disconnects.
func (r *MusicRelay) SetActive(pr *os.File, active bool) {
	r.mu.Lock()
	for c := range r.clients {
		if c.pr == pr {
			if active && !c.active {
				c.active = true
				r.active++
				if r.active == 1 {
					select {
					case r.wakeCh <- struct{}{}:
					default:
					}
				}
			} else if !active && c.active {
				c.active = false
				r.active--
			}
			break
		}
	}
	r.mu.Unlock()
}

// runRelayWriter is the writer goroutine for a relay client.  It reads chunks
// from the client's channel and writes them to the OS pipe.  When the channel
// is closed, the goroutine exits.
func runRelayWriter(c *relayClient) {
	defer close(c.done)
	defer c.pw.Close()
	for data := range c.ch {
		if _, err := c.pw.Write(data); err != nil {
			return
		}
	}
}

func (r *MusicRelay) clientCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.clients)
}

func (r *MusicRelay) isActive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active > 0
}

// run is the main loop that fetches from the stream URL and reconnects on error.
func (r *MusicRelay) run() {
	for {
		r.mu.Lock()
		stopCh := r.stopCh
		r.mu.Unlock()

		select {
		case <-stopCh:
			return
		default:
		}

		// Wait until at least one subscriber is active before connecting.
		// Loop to discard stale wake signals that arrived after the
		// previous fetch() disconnected but before isActive went true.
		logged := false
		for !r.isActive() {
			if !logged {
				log.Printf("music relay: idle, waiting for active viewer for %s", r.url)
				logged = true
			}
			select {
			case <-r.wakeCh:
			case <-stopCh:
				return
			}
		}

		r.fetch(stopCh)
	}
}

// fetch opens a single HTTP connection and broadcasts data until error,
// stop, or all subscribers become inactive.
func (r *MusicRelay) fetch(stopCh chan struct{}) {
	log.Printf("music relay: connecting to %s", r.url)

	// Derive a context that is cancelled when stopCh closes, so the HTTP
	// request (including blocked reads on a stalled stream) is interrupted.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	req, err := http.NewRequestWithContext(ctx, "GET", r.url, nil)
	if err != nil {
		log.Printf("music relay: bad URL %s: %v", r.url, err)
		return
	}
	// Icecast servers often reject connections without a recognized User-Agent.
	req.Header.Set("User-Agent", "FFmpeg/7.0")
	req.Header.Set("Icy-MetaData", "0")

	resp, err := r.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down
		}
		log.Printf("music relay: connect error: %v (retrying in 5s)", err)
		select {
		case <-time.After(5 * time.Second):
		case <-stopCh:
		}
		return
	}
	defer resp.Body.Close()

	log.Printf("music relay: connected to %s (status %d)", r.url, resp.StatusCode)

	buf := make([]byte, relayChunkSize)
	var bytesReceived int64
	var lastKBps float64
	throughputTick := time.NewTicker(60 * time.Second)
	defer throughputTick.Stop()
	lastReport := time.Now()

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		// Disconnect when all subscribers are idle to save bandwidth.
		if !r.isActive() {
			log.Printf("music relay: all subscribers idle, disconnecting from %s", r.url)
			return
		}

		// Log periodic throughput summary — only when rate changes significantly.
		select {
		case <-throughputTick.C:
			elapsed := time.Since(lastReport).Seconds()
			if elapsed > 0 {
				kbps := float64(bytesReceived) / elapsed / 1024
				// Log on first report or when throughput changes by >10%.
				if lastKBps == 0 || math.Abs(kbps-lastKBps)/lastKBps > 0.1 {
					log.Printf("music relay: throughput %.1f KB/s from %s", kbps, r.url)
				}
				lastKBps = kbps
			}
			bytesReceived = 0
			lastReport = time.Now()
		default:
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			bytesReceived += int64(n)
			data := make([]byte, n)
			copy(data, buf[:n])
			r.broadcast(data)
		}
		if err != nil {
			if ctx.Err() != nil {
				return // shutting down (all subscribers removed)
			}
			if err != io.EOF {
				log.Printf("music relay: read error: %v (reconnecting)", err)
			} else {
				log.Printf("music relay: stream ended (reconnecting)")
			}
			// Brief pause before reconnect to avoid tight loop.
			select {
			case <-time.After(2 * time.Second):
			case <-stopCh:
			}
			return
		}
	}
}

// Drops returns the number of audio chunks dropped for the subscriber
// identified by the read end of its pipe. Returns 0 if not found.
func (r *MusicRelay) Drops(pr *os.File) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.clients {
		if c.pr == pr {
			return c.drops
		}
	}
	return 0
}

// Received returns the number of audio chunks successfully queued for the
// subscriber identified by the read end of its pipe. Returns 0 if not found.
func (r *MusicRelay) Received(pr *os.File) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.clients {
		if c.pr == pr {
			return c.received
		}
	}
	return 0
}

func (r *MusicRelay) broadcast(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.clients {
		if !c.active {
			continue // skip inactive subscribers — their FFmpeg is suspended
		}
		select {
		case c.ch <- data:
			c.received++
		default:
			c.drops++
			if c.drops%500 == 1 {
				log.Printf("music relay: dropping audio chunks for a subscriber (%d total drops) — pipe backpressure", c.drops)
			}
		}
	}
}
