package stream

import (
	"errors"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// chunkSize is ~3KB per read (188 bytes = 1 MPEG-TS packet, 16 packets per chunk).
	chunkSize = 188 * 16
	// clientBufLen is how many chunks to buffer per client before dropping.
	// At ~3KB each, 512 chunks ≈ 1.5 MB ≈ 6 seconds of buffering at 2 Mbit/s.
	clientBufLen = 512
)

// Hub reads a byte stream from a single source (FFmpeg stdout) and broadcasts
// each chunk to all connected HTTP clients.
type Hub struct {
	mu          sync.RWMutex
	clients     map[chan []byte]struct{}
	clientDrops map[chan []byte]*atomic.Int64 // per-client drop counter
	connectTime map[chan []byte]time.Time     // when each client connected
	viewTotal   time.Duration                 // accumulated viewing time from disconnected clients
	viewCount   int                           // total number of connections (subscribe calls)
	dropsTotal  int64                         // accumulated drops from disconnected clients

	// Stream health counters (lock-free atomics, updated on broadcast hot path).
	chunkCount    atomic.Int64 // total chunks broadcast
	bytesTotal    atomic.Int64 // total bytes broadcast
	lastBroadcast atomic.Int64 // UnixMilli of most recent broadcast

	// activatedAt is set when the hub transitions from idle to active.
	// Chunks arriving within flushWindow after activation are discarded,
	// draining stale data from FFmpeg's pipe buffer after a SIGCONT resume.
	activatedAt time.Time

	// OnActive is called (without lock held) when the first client connects.
	// OnIdle is called (without lock held) when the last client disconnects.
	OnActive func()
	OnIdle   func()
}

// flushWindow is how long after activation the hub discards stale data
// from FFmpeg's internal and OS pipe buffers before broadcasting to clients.
// Derived from AudioThreadQueueSize: each MP3 packet is ~26ms, and we add
// 500ms of margin for the muxer to flush the interleaved output.
const flushWindow = time.Duration(AudioThreadQueueSize)*26*time.Millisecond + 500*time.Millisecond

// ResetFlushWindow restarts the flush window from now.  Call this just
// before resuming FFmpeg so the window covers the actual resume moment
// rather than the (potentially much earlier) Subscribe call.
func (h *Hub) ResetFlushWindow() {
	h.mu.Lock()
	h.activatedAt = time.Now()
	h.mu.Unlock()
}

// NewHub creates a ready-to-use Hub.
func NewHub() *Hub {
	return &Hub{
		clients:     make(map[chan []byte]struct{}),
		clientDrops: make(map[chan []byte]*atomic.Int64),
		connectTime: make(map[chan []byte]time.Time),
	}
}

// Run reads from r until EOF or error, broadcasting each chunk to all clients.
// Should be run in a goroutine. Returns when the reader is exhausted.
func (h *Hub) Run(r io.Reader) {
	buf := make([]byte, chunkSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			h.broadcast(buf[:n])
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("stream: broadcast hub reader error: %v", err)
			}
			return
		}
	}
}

// Subscribe registers a new client channel. The caller must call Unsubscribe when done.
func (h *Hub) Subscribe() chan []byte {
	ch := make(chan []byte, clientBufLen)
	var activate bool
	h.mu.Lock()
	activate = len(h.clients) == 0 && h.OnActive != nil
	if activate {
		h.activatedAt = time.Now()
	}
	h.clients[ch] = struct{}{}
	h.clientDrops[ch] = &atomic.Int64{}
	h.connectTime[ch] = time.Now()
	h.viewCount++
	h.mu.Unlock()
	if activate {
		h.OnActive()
	}
	return ch
}

// Unsubscribe removes a client channel and closes it.
func (h *Hub) Unsubscribe(ch chan []byte) {
	var deactivate bool
	h.mu.Lock()
	if t, ok := h.connectTime[ch]; ok {
		h.viewTotal += time.Since(t)
		delete(h.connectTime, ch)
	}
	if d, ok := h.clientDrops[ch]; ok {
		h.dropsTotal += d.Load()
		delete(h.clientDrops, ch)
	}
	delete(h.clients, ch)
	deactivate = len(h.clients) == 0 && h.OnIdle != nil
	h.mu.Unlock()
	close(ch)
	if deactivate {
		h.OnIdle()
	}
}

func (h *Hub) broadcast(chunk []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// After a resume from suspension, FFmpeg's pipe buffer contains stale
	// encoded data from before the pause. Discard it so clients don't see
	// a flash of old frames before fresh content arrives.
	if !h.activatedAt.IsZero() && time.Since(h.activatedAt) < flushWindow {
		return
	}

	h.chunkCount.Add(1)
	h.bytesTotal.Add(int64(len(chunk)))
	h.lastBroadcast.Store(time.Now().UnixMilli())

	// Copy chunk so each client receives its own slice (the read buffer will be reused).
	data := make([]byte, len(chunk))
	copy(data, chunk)

	for ch := range h.clients {
		select {
		case ch <- data:
		default:
			d := h.clientDrops[ch]
			n := d.Add(1)
			if n%500 == 1 {
				log.Printf("stream: hub dropping chunks for a slow client (%d total drops)", n)
			}
		}
	}
}

// ClientCount returns the number of currently connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// TotalViews returns the total number of client connections since the hub started.
func (h *Hub) TotalViews() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.viewCount
}

// TotalViewTime returns the cumulative viewing time across all clients
// (disconnected + still-connected).
func (h *Hub) TotalViewTime() time.Duration {
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := h.viewTotal
	now := time.Now()
	for _, t := range h.connectTime {
		total += now.Sub(t)
	}
	return total
}

// ClientDrops returns the total number of chunks dropped across all clients
// (current + disconnected) due to slow consumption.
func (h *Hub) ClientDrops() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := h.dropsTotal
	for _, d := range h.clientDrops {
		total += d.Load()
	}
	return total
}

// HubDiagnostics is a point-in-time snapshot of stream health counters.
type HubDiagnostics struct {
	ChunkCount   int64
	BytesTotal   int64
	SecSinceSend float64 // 0 if never broadcast
	KBps         float64 // avg KB/s (bytesTotal / totalViewTime)
}

// Diagnostics returns a snapshot of stream health counters.
func (h *Hub) Diagnostics() HubDiagnostics {
	chunks := h.chunkCount.Load()
	total := h.bytesTotal.Load()
	last := h.lastBroadcast.Load()

	var secSince float64
	if last > 0 {
		secSince = float64(time.Now().UnixMilli()-last) / 1000.0
	}

	var kbps float64
	if vt := h.TotalViewTime(); vt > 0 {
		kbps = float64(total) / 1024.0 / vt.Seconds()
	}

	return HubDiagnostics{
		ChunkCount:   chunks,
		BytesTotal:   total,
		SecSinceSend: secSince,
		KBps:         kbps,
	}
}

// ServeHTTP streams MPEG-TS to an HTTP client for the lifetime of the connection.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	ch := h.Subscribe()
	defer h.Unsubscribe(ch)

	log.Printf("stream: client connected from %s | User-Agent: %s | X-Forwarded-For: %s | Referer: %s",
		r.RemoteAddr, r.UserAgent(), r.Header.Get("X-Forwarded-For"), r.Header.Get("Referer"))
	defer log.Printf("stream: client disconnected from %s", r.RemoteAddr)

	for {
		select {
		case <-r.Context().Done():
			return
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(chunk); err != nil {
				if errors.Is(err, io.ErrClosedPipe) || r.Context().Err() != nil {
					log.Printf("stream: client %s disconnected (write)", r.RemoteAddr)
				} else {
					log.Printf("stream: write error for client %s: %v", r.RemoteAddr, err)
				}
				return
			}
			flusher.Flush()
		}
	}
}
