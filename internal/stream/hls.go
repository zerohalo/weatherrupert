package stream

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const mpegTSPacketSize = 188

// isKeyframeAt reports whether the MPEG-TS packet at data[offset] has the
// random_access_indicator bit set (i.e. starts an H.264 keyframe).
func isKeyframeAt(data []byte, offset int) bool {
	if offset+6 > len(data) {
		return false
	}
	return data[offset] == 0x47 && // sync byte
		data[offset+3]&0x20 != 0 && // adaptation field present
		data[offset+4] > 0 && // adaptation field has content
		data[offset+5]&0x40 != 0 // random_access_indicator
}

// syncTS finds the first MPEG-TS sync point at or after start by looking for
// 0x47 bytes that repeat at 188-byte intervals (standard MPEG-TS sync).
func syncTS(data []byte, start int) int {
	for off := start; off+mpegTSPacketSize < len(data); off++ {
		if data[off] == 0x47 && data[off+mpegTSPacketSize] == 0x47 {
			return off
		}
	}
	return -1
}

// findKeyframe walks data in 188-byte MPEG-TS packet steps starting from
// start and returns the offset of the first packet with
// random_access_indicator set. Returns -1 if none found.
func findKeyframe(data []byte, start int) int {
	off := syncTS(data, start)
	if off < 0 {
		return -1
	}
	for off+mpegTSPacketSize <= len(data) {
		if isKeyframeAt(data, off) {
			return off
		}
		off += mpegTSPacketSize
	}
	return -1
}

// Segment holds a single HLS MPEG-TS segment.
type Segment struct {
	SeqNum   int
	Duration float64 // actual duration in seconds
	Data     []byte  // MPEG-TS payload
}

// HLSSegmenter subscribes to a Hub to accumulate MPEG-TS data into fixed-duration
// segments, serving them as an HLS live stream via ServePlaylist and ServeSegment.
//
// It dynamically subscribes/unsubscribes from the Hub based on whether any HLS
// clients are polling, so FFmpeg idles when nobody is watching.
type HLSSegmenter struct {
	hub             *Hub
	segmentDuration time.Duration
	playlistSize    int
	ringSize        int

	// Query parameters baked into playlist segment URLs.
	zip, clock, units string

	mu       sync.RWMutex
	ring     []*Segment // ring buffer of recent segments
	ringPos  int        // next write position in ring
	seqNum   int        // next sequence number to assign
	accumBuf []byte     // accumulator for current in-progress segment
	accumT   time.Time  // when accumulation of current segment started

	// Hub subscription state (protected by mu).
	ch           chan []byte // non-nil when subscribed
	subscribed   bool
	subscribedAt time.Time     // when the current subscription started
	hubSubs      int           // total number of hub subscriptions
	hubSubTime   time.Duration // accumulated hub subscription time from past subscriptions
	ready        chan struct{} // closed when first segment available after subscribe
	readyOnce    sync.Once
	readyClosed  bool

	stopped  atomic.Bool  // true after Run exits (context cancelled)
	lastPoll atomic.Int64 // UnixMilli of most recent HLS request

	// Segment production stats (protected by mu).
	segCount    int       // total segments produced
	segSizeMin  int       // smallest segment bytes (init to max int)
	segSizeMax  int       // largest segment bytes
	segSizeSum  int64     // sum of all segment sizes for avg
	lastSegTime time.Time // when most recent segment was finalized

	// Request counters (atomics — lock-free on hot path).
	playlistReqs  atomic.Int64 // total playlist requests served
	segmentReqs   atomic.Int64 // total segment requests served
	segmentMisses atomic.Int64 // segment 404s (expired from ring)

	// Live-edge lag (atomics — updated in ServeSegment).
	lagSum atomic.Int64 // sum of (latestSeq - requestedSeq)
	lagN   atomic.Int64 // number of lag samples
	lagMax atomic.Int64 // worst lag observed

	// View tracking for HLS clients specifically.
	viewMu        sync.Mutex
	viewCount     int
	viewTotal     time.Duration
	activeViewers map[string]time.Time // remoteAddr → connect time
}

// NewHLSSegmenter creates a new segmenter. Call Run() in a goroutine to start it.
func NewHLSSegmenter(hub *Hub, zip, clock, units string, segDuration time.Duration, playlistSize, ringSize int) *HLSSegmenter {
	return &HLSSegmenter{
		hub:             hub,
		segmentDuration: segDuration,
		playlistSize:    playlistSize,
		ringSize:        ringSize,
		zip:             zip,
		clock:           clock,
		units:           units,
		ring:            make([]*Segment, ringSize),
		activeViewers:   make(map[string]time.Time),
	}
}

// SubscribeNow subscribes to the Hub immediately so segments start
// accumulating before any HLS client connects (warmup).
func (s *HLSSegmenter) SubscribeNow() {
	s.subscribe()
}

// Run is the main loop. It must be run in a goroutine. It blocks until ctx is cancelled.
func (s *HLSSegmenter) Run(ctx context.Context) {
	defer s.stopped.Store(true)

	idleTicker := time.NewTicker(1 * time.Second)
	defer idleTicker.Stop()

	for {
		s.mu.RLock()
		ch := s.ch
		s.mu.RUnlock()

		if ch != nil {
			// Subscribed: read chunks and check idle.
			select {
			case <-ctx.Done():
				s.unsubscribe()
				return
			case chunk, ok := <-ch:
				if !ok {
					// Hub closed the channel (e.g. FFmpeg restarted).
					s.mu.Lock()
					s.ch = nil
					s.subscribed = false
					s.accumBuf = nil
					s.mu.Unlock()
					continue
				}
				s.ingestChunk(chunk)
			case <-idleTicker.C:
				s.checkIdle()
			}
		} else {
			// Unsubscribed: just wait for wake or shutdown.
			select {
			case <-ctx.Done():
				return
			case <-idleTicker.C:
				// Nothing to do; checkIdle is a no-op when unsubscribed.
			}
		}
	}
}

// ingestChunk appends data to the accumulator and finalizes a segment when
// enough time has elapsed and a keyframe boundary is found. Splitting on
// keyframes ensures each HLS segment starts at a random access point so
// players (VLC, Safari, etc.) can decode immediately.
func (s *HLSSegmenter) ingestChunk(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.accumT.IsZero() {
		s.accumT = now
	}

	prevLen := len(s.accumBuf)
	s.accumBuf = append(s.accumBuf, chunk...)

	elapsed := now.Sub(s.accumT)
	if elapsed < s.segmentDuration || len(s.accumBuf) == 0 {
		return
	}

	// Scan newly appended data for a keyframe to split on.
	kf := findKeyframe(s.accumBuf, prevLen)
	if kf < 0 {
		if len(s.accumBuf)%10000 < len(chunk) { // log periodically
			log.Printf("hls: no keyframe yet, accumBuf=%d bytes, elapsed=%v, prevLen=%d (zip %s)",
				len(s.accumBuf), elapsed, prevLen, s.zip)
		}
		return // no keyframe yet, keep accumulating
	}

	log.Printf("hls: splitting segment at keyframe offset %d, accumBuf=%d bytes, elapsed=%v (zip %s)",
		kf, len(s.accumBuf), elapsed, s.zip)

	// Split: everything before the keyframe is the segment,
	// the keyframe and beyond starts the next segment.
	segData := make([]byte, kf)
	copy(segData, s.accumBuf[:kf])
	remaining := make([]byte, len(s.accumBuf)-kf)
	copy(remaining, s.accumBuf[kf:])

	s.accumBuf = remaining
	s.finalizeSegmentData(now, segData)
}

// finalizeSegmentData stores segData as a segment in the ring buffer and
// resets the accumulation start time. Must be called with s.mu held.
func (s *HLSSegmenter) finalizeSegmentData(now time.Time, segData []byte) {
	// Use the configured segment duration rather than wall-clock elapsed time.
	// We split on keyframes aligned to segmentDuration (via FFmpeg -g), so each
	// segment contains exactly one keyframe interval of video. Wall-clock time
	// includes processing overhead that inflates EXTINF, causing players to
	// expect more video than exists and stutter at segment boundaries.
	dur := s.segmentDuration.Seconds()
	seg := &Segment{
		SeqNum:   s.seqNum,
		Duration: dur,
		Data:     segData,
	}

	s.ring[s.ringPos] = seg
	s.ringPos = (s.ringPos + 1) % s.ringSize
	s.seqNum++
	s.accumT = now

	// Update segment production stats.
	size := len(segData)
	s.segCount++
	s.segSizeSum += int64(size)
	if size < s.segSizeMin || s.segSizeMin == 0 {
		s.segSizeMin = size
	}
	if size > s.segSizeMax {
		s.segSizeMax = size
	}
	s.lastSegTime = now

	// Signal that at least one segment is available.
	if !s.readyClosed {
		s.readyOnce.Do(func() { close(s.ready) })
		s.readyClosed = true
	}
}

// subscribe connects to the Hub if not already subscribed.
func (s *HLSSegmenter) subscribe() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.subscribed {
		return
	}
	s.ch = s.hub.Subscribe()
	s.subscribed = true
	s.subscribedAt = time.Now()
	s.hubSubs++
	s.accumBuf = nil
	s.accumT = time.Time{}
	// Clear stale segments from the ring. After a suspend/resume gap,
	// old segments contain outdated audio/video that causes discontinuity
	// artifacts (audio dropout or stream stalls) when mixed with fresh
	// segments. Wait for new segments instead (~3s for one segment).
	for i := range s.ring {
		s.ring[i] = nil
	}
	s.ringPos = 0
	s.ready = make(chan struct{})
	s.readyOnce = sync.Once{}
	s.readyClosed = false
	log.Printf("hls: segmenter subscribed to hub (zip %s)", s.zip)
}

// unsubscribe disconnects from the Hub if currently subscribed.
func (s *HLSSegmenter) unsubscribe() {
	s.mu.Lock()
	if !s.subscribed {
		s.mu.Unlock()
		return
	}
	ch := s.ch
	s.ch = nil
	s.subscribed = false
	s.hubSubTime += time.Since(s.subscribedAt)
	s.accumBuf = nil
	// Close ready channel if still open so any waiters unblock.
	if !s.readyClosed {
		s.readyOnce.Do(func() { close(s.ready) })
		s.readyClosed = true
	}
	s.mu.Unlock()

	s.hub.Unsubscribe(ch)
	log.Printf("hls: segmenter unsubscribed from hub (zip %s)", s.zip)
}

// checkIdle unsubscribes if no HLS client has polled recently.
func (s *HLSSegmenter) checkIdle() {
	s.mu.RLock()
	subscribed := s.subscribed
	s.mu.RUnlock()

	if !subscribed {
		return
	}

	last := s.lastPoll.Load()
	if last == 0 {
		return
	}
	if time.Since(time.UnixMilli(last)) > 2*s.segmentDuration {
		s.unsubscribe()
		// Clean up view tracking for any stale viewers.
		s.viewMu.Lock()
		now := time.Now()
		for addr, t := range s.activeViewers {
			s.viewTotal += now.Sub(t)
			delete(s.activeViewers, addr)
		}
		s.viewMu.Unlock()
	}
}

// touchPoll records a poll, subscribes if needed, and returns the ready channel.
func (s *HLSSegmenter) touchPoll() chan struct{} {
	s.lastPoll.Store(time.Now().UnixMilli())
	s.subscribe()
	s.mu.RLock()
	ready := s.ready
	s.mu.RUnlock()
	return ready
}

// ServePlaylist handles GET /live.m3u8 requests.
func (s *HLSSegmenter) ServePlaylist(w http.ResponseWriter, r *http.Request) {
	ready := s.touchPoll()
	s.playlistReqs.Add(1)

	// Track this viewer.
	addr := r.RemoteAddr
	s.viewMu.Lock()
	if _, ok := s.activeViewers[addr]; !ok {
		s.activeViewers[addr] = time.Now()
		s.viewCount++
	}
	s.viewMu.Unlock()

	// Wait for at least one segment to be available (bootstrap delay).
	select {
	case <-ready:
	case <-r.Context().Done():
		return
	case <-time.After(10 * time.Second):
		log.Printf("hls: playlist timeout waiting for first segment (zip %s)", s.zip)
		http.Error(w, "timeout waiting for stream", http.StatusServiceUnavailable)
		return
	}

	// Refresh lastPoll after the bootstrap wait so the idle checker doesn't
	// fire while we were blocked waiting for the first segment.
	s.lastPoll.Store(time.Now().UnixMilli())

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect available segments for the playlist.
	segments := s.recentSegments()
	if len(segments) == 0 {
		log.Printf("hls: playlist has no segments after ready (zip %s)", s.zip)
		http.Error(w, "no segments available", http.StatusServiceUnavailable)
		return
	}

	// Build M3U8 playlist.
	targetDur := int(s.segmentDuration.Seconds())
	if targetDur < 1 {
		targetDur = 1
	}
	mediaSeq := segments[0].SeqNum

	playlist := fmt.Sprintf("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n", targetDur, mediaSeq)
	for _, seg := range segments {
		playlist += fmt.Sprintf("#EXTINF:%.3f,\nsegment?zip=%s&clock=%s&units=%s&seq=%d\n", seg.Duration, s.zip, s.clock, s.units, seg.SeqNum)
	}
	if s.stopped.Load() {
		playlist += "#EXT-X-ENDLIST\n"
	}

	log.Printf("hls: serving playlist with %d segment(s), mediaSeq=%d (zip %s)", len(segments), mediaSeq, s.zip)
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Write([]byte(playlist))
}

// ServeSegment handles GET /segment requests.
func (s *HLSSegmenter) ServeSegment(w http.ResponseWriter, r *http.Request) {
	s.touchPoll()

	seqStr := r.URL.Query().Get("seq")
	seq, err := strconv.Atoi(seqStr)
	if err != nil {
		http.Error(w, "invalid seq parameter", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, seg := range s.ring {
		if seg != nil && seg.SeqNum == seq {
			s.segmentReqs.Add(1)
			lag := int64(s.seqNum-1) - int64(seq)
			if lag < 0 {
				lag = 0
			}
			s.lagSum.Add(lag)
			s.lagN.Add(1)
			// CAS update for lagMax.
			for {
				cur := s.lagMax.Load()
				if lag <= cur {
					break
				}
				if s.lagMax.CompareAndSwap(cur, lag) {
					break
				}
			}
			log.Printf("hls: serving segment seq=%d (%d bytes, lag=%d) (zip %s)", seq, len(seg.Data), lag, s.zip)
			w.Header().Set("Content-Type", "video/mp2t")
			w.Header().Set("Cache-Control", "max-age=3")
			w.Write(seg.Data)
			return
		}
	}

	s.segmentMisses.Add(1)
	log.Printf("hls: segment seq=%d not found in ring (zip %s)", seq, s.zip)
	http.Error(w, "segment not found", http.StatusNotFound)
}

// recentSegments returns up to playlistSize segments in sequence order.
// Must be called with s.mu held (at least RLock).
func (s *HLSSegmenter) recentSegments() []*Segment {
	// Collect all non-nil segments.
	var all []*Segment
	for _, seg := range s.ring {
		if seg != nil {
			all = append(all, seg)
		}
	}
	if len(all) == 0 {
		return nil
	}

	// Sort by sequence number (ring may wrap).
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].SeqNum < all[j-1].SeqNum; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}

	// Return only the most recent playlistSize segments.
	if len(all) > s.playlistSize {
		all = all[len(all)-s.playlistSize:]
	}
	return all
}

// ClientCount returns 1 if the segmenter is subscribed to the hub (has active
// HLS viewers), 0 otherwise. Used for admin panel stats.
func (s *HLSSegmenter) ClientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.subscribed {
		return 1
	}
	return 0
}

// HubSubscriptions returns the number of times the segmenter has subscribed to
// the Hub. Subtract from hub.TotalViews() to get direct MPEG-TS view count.
func (s *HLSSegmenter) HubSubscriptions() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hubSubs
}

// HubSubscriptionTime returns the cumulative time the segmenter has been
// subscribed to the Hub. Subtract from hub.TotalViewTime() to get direct
// MPEG-TS viewing time.
func (s *HLSSegmenter) HubSubscriptionTime() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := s.hubSubTime
	if s.subscribed {
		total += time.Since(s.subscribedAt)
	}
	return total
}

// TotalViews returns the total number of HLS viewer sessions.
func (s *HLSSegmenter) TotalViews() int {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	return s.viewCount
}

// TotalViewTime returns cumulative HLS viewing time.
func (s *HLSSegmenter) TotalViewTime() time.Duration {
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	total := s.viewTotal
	now := time.Now()
	for _, t := range s.activeViewers {
		total += now.Sub(t)
	}
	return total
}

// HLSDiagnostics is a point-in-time snapshot of HLS health metrics.
type HLSDiagnostics struct {
	SegCount      int
	SegSizeMin    int // 0 if none produced
	SegSizeMax    int
	SegSizeAvg    int
	SecSinceSeg   float64 // 0 if never produced
	PlaylistReqs  int64
	SegmentReqs   int64
	SegmentMisses int64
	LagAvg        float64
	LagMax        int64
}

// Diagnostics returns a snapshot of HLS health metrics.
func (s *HLSSegmenter) Diagnostics() HLSDiagnostics {
	s.mu.RLock()
	d := HLSDiagnostics{
		SegCount:   s.segCount,
		SegSizeMin: s.segSizeMin,
		SegSizeMax: s.segSizeMax,
	}
	if s.segCount > 0 {
		d.SegSizeAvg = int(s.segSizeSum / int64(s.segCount))
		d.SecSinceSeg = time.Since(s.lastSegTime).Seconds()
	}
	s.mu.RUnlock()

	d.PlaylistReqs = s.playlistReqs.Load()
	d.SegmentReqs = s.segmentReqs.Load()
	d.SegmentMisses = s.segmentMisses.Load()
	d.LagMax = s.lagMax.Load()
	if n := s.lagN.Load(); n > 0 {
		d.LagAvg = float64(s.lagSum.Load()) / float64(n)
		d.LagAvg = math.Round(d.LagAvg*10) / 10
	}
	return d
}
