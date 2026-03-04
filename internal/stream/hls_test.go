package stream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestSegmenter() (*HLSSegmenter, *Hub) {
	hub := NewHub()
	seg := NewHLSSegmenter(hub, "90210", "12", "imperial", 100*time.Millisecond, 3, 5)
	return seg, hub
}

// makeTSPacket builds a 188-byte MPEG-TS packet. If keyframe is true, the
// random_access_indicator bit is set in the adaptation field.
func makeTSPacket(keyframe bool) []byte {
	pkt := make([]byte, mpegTSPacketSize)
	pkt[0] = 0x47 // sync byte
	if keyframe {
		pkt[3] = 0x30 // adaptation field + payload
		pkt[4] = 1    // adaptation field length
		pkt[5] = 0x40 // random_access_indicator
	}
	return pkt
}

// makeTSChunk builds a chunk of n MPEG-TS packets. The first packet has the
// keyframe flag set if keyframe is true.
func makeTSChunk(n int, keyframe bool) []byte {
	var buf []byte
	for i := 0; i < n; i++ {
		buf = append(buf, makeTSPacket(i == 0 && keyframe)...)
	}
	return buf
}

func TestHLSSegmenterSubscribesOnPoll(t *testing.T) {
	seg, hub := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	if hub.ClientCount() != 0 {
		t.Fatal("hub should have 0 clients before any poll")
	}

	seg.touchPoll()
	time.Sleep(20 * time.Millisecond) // let Run loop pick up state

	if hub.ClientCount() != 1 {
		t.Errorf("hub.ClientCount() = %d, want 1 after touchPoll", hub.ClientCount())
	}
}

func TestHLSSegmenterUnsubscribesOnIdle(t *testing.T) {
	seg, hub := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	seg.touchPoll()
	time.Sleep(20 * time.Millisecond)

	if hub.ClientCount() != 1 {
		t.Fatal("expected 1 client after subscribe")
	}

	// Wait for idle timeout (2× segment duration = 200ms) + check interval (1s is too slow,
	// but our segment duration is 100ms so idle is 200ms).
	time.Sleep(1200 * time.Millisecond)

	if hub.ClientCount() != 0 {
		t.Errorf("hub.ClientCount() = %d, want 0 after idle", hub.ClientCount())
	}
}

func TestHLSSegmenterRingBuffer(t *testing.T) {
	seg, _ := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	seg.touchPoll()
	time.Sleep(20 * time.Millisecond)

	// Feed data and let segments form. With 100ms segment duration,
	// send chunks over ~400ms to get at least 3 segments.
	// Each chunk starts with a keyframe so the segmenter can split.
	for i := 0; i < 8; i++ {
		seg.ingestChunk(makeTSChunk(4, true))
		time.Sleep(60 * time.Millisecond)
	}

	seg.mu.RLock()
	segments := seg.recentSegments()
	seg.mu.RUnlock()

	if len(segments) == 0 {
		t.Fatal("expected at least 1 segment")
	}
	if len(segments) > 3 {
		t.Errorf("recentSegments returned %d, want <= 3 (playlistSize)", len(segments))
	}

	// Verify segments are in sequence order.
	for i := 1; i < len(segments); i++ {
		if segments[i].SeqNum <= segments[i-1].SeqNum {
			t.Errorf("segments not in order: seq %d after %d", segments[i].SeqNum, segments[i-1].SeqNum)
		}
	}
}

func TestHLSSegmenterServePlaylist(t *testing.T) {
	seg, _ := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	seg.touchPoll()
	time.Sleep(20 * time.Millisecond)

	// Feed enough data to produce a segment.
	for i := 0; i < 4; i++ {
		seg.ingestChunk(makeTSChunk(4, true))
		time.Sleep(40 * time.Millisecond)
	}

	req := httptest.NewRequest("GET", "/live.m3u8?zip=90210", nil)
	rec := httptest.NewRecorder()
	seg.ServePlaylist(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "#EXTM3U") {
		t.Error("playlist missing #EXTM3U header")
	}
	if !strings.Contains(body, "#EXT-X-TARGETDURATION:") {
		t.Error("playlist missing #EXT-X-TARGETDURATION")
	}
	if !strings.Contains(body, "segment?zip=90210") {
		t.Error("playlist missing segment URL")
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type = %q, want application/vnd.apple.mpegurl", ct)
	}
}

func TestHLSSegmenterServeSegment(t *testing.T) {
	seg, _ := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	seg.touchPoll()
	time.Sleep(20 * time.Millisecond)

	// Produce segments.
	for i := 0; i < 6; i++ {
		seg.ingestChunk(makeTSChunk(4, true))
		time.Sleep(40 * time.Millisecond)
	}

	seg.mu.RLock()
	segments := seg.recentSegments()
	seg.mu.RUnlock()

	if len(segments) == 0 {
		t.Fatal("no segments available")
	}

	seqNum := segments[0].SeqNum
	req := httptest.NewRequest("GET", "/segment?zip=90210&seq="+strconv.Itoa(seqNum), nil)
	rec := httptest.NewRecorder()
	seg.ServeSegment(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("segment body is empty")
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "video/mp2t" {
		t.Errorf("Content-Type = %q, want video/mp2t", ct)
	}
}

func TestHLSSegmenterServeSegmentNotFound(t *testing.T) {
	seg, _ := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	req := httptest.NewRequest("GET", "/segment?zip=90210&seq=99999", nil)
	rec := httptest.NewRecorder()
	seg.ServeSegment(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHLSSegmenterClientCount(t *testing.T) {
	seg, _ := newTestSegmenter()

	if seg.ClientCount() != 0 {
		t.Error("ClientCount should be 0 when not subscribed")
	}

	seg.subscribe()
	if seg.ClientCount() != 1 {
		t.Error("ClientCount should be 1 when subscribed")
	}

	seg.unsubscribe()
	if seg.ClientCount() != 0 {
		t.Error("ClientCount should be 0 after unsubscribe")
	}
}

func TestHLSSegmenterHubSubscriptionTracking(t *testing.T) {
	seg, _ := newTestSegmenter()

	seg.subscribe()
	time.Sleep(50 * time.Millisecond)
	seg.unsubscribe()

	if seg.HubSubscriptions() != 1 {
		t.Errorf("HubSubscriptions = %d, want 1", seg.HubSubscriptions())
	}
	if seg.HubSubscriptionTime() < 40*time.Millisecond {
		t.Errorf("HubSubscriptionTime = %v, expected >= 40ms", seg.HubSubscriptionTime())
	}

	seg.subscribe()
	time.Sleep(50 * time.Millisecond)
	seg.unsubscribe()

	if seg.HubSubscriptions() != 2 {
		t.Errorf("HubSubscriptions = %d, want 2", seg.HubSubscriptions())
	}
}

func TestIsKeyframeAt(t *testing.T) {
	pkt := makeTSPacket(true)
	if !isKeyframeAt(pkt, 0) {
		t.Error("expected keyframe packet to be detected")
	}

	pkt2 := makeTSPacket(false)
	if isKeyframeAt(pkt2, 0) {
		t.Error("non-keyframe packet should not be detected as keyframe")
	}

	// Too short.
	if isKeyframeAt([]byte{0x47, 0, 0}, 0) {
		t.Error("short data should not be detected as keyframe")
	}
}

func TestSyncTS(t *testing.T) {
	// Two consecutive packets — sync should find offset 0.
	buf := append(makeTSPacket(false), makeTSPacket(false)...)
	if off := syncTS(buf, 0); off != 0 {
		t.Errorf("syncTS = %d, want 0", off)
	}

	// Start mid-packet — need 3 packets so offset 188 can be verified against 376.
	buf3 := makeTSChunk(3, false)
	if off := syncTS(buf3, 10); off != mpegTSPacketSize {
		t.Errorf("syncTS from mid-packet = %d, want %d", off, mpegTSPacketSize)
	}

	// Single packet (no second sync byte to verify) — should return -1.
	single := makeTSPacket(false)
	if off := syncTS(single, 0); off != -1 {
		t.Errorf("syncTS single packet = %d, want -1", off)
	}
}

func TestFindKeyframe(t *testing.T) {
	// Build buffer: 2 non-keyframe packets then 1 keyframe packet + trailing packet for sync.
	var buf []byte
	buf = append(buf, makeTSPacket(false)...)
	buf = append(buf, makeTSPacket(false)...)
	buf = append(buf, makeTSPacket(true)...)
	buf = append(buf, makeTSPacket(false)...) // needed for sync verification

	off := findKeyframe(buf, 0)
	if off != 2*mpegTSPacketSize {
		t.Errorf("findKeyframe = %d, want %d", off, 2*mpegTSPacketSize)
	}

	// No keyframe in buffer.
	buf2 := append(makeTSPacket(false), makeTSPacket(false)...)
	if findKeyframe(buf2, 0) != -1 {
		t.Error("expected -1 when no keyframe present")
	}

	// Start scanning from mid-packet offset — should still find keyframe.
	off2 := findKeyframe(buf, 50)
	if off2 != 2*mpegTSPacketSize {
		t.Errorf("findKeyframe from mid-packet = %d, want %d", off2, 2*mpegTSPacketSize)
	}
}

func TestIngestChunkSplitsOnKeyframe(t *testing.T) {
	seg, _ := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	seg.touchPoll()
	time.Sleep(20 * time.Millisecond)

	// First chunk: keyframe + data (starts accumulation).
	seg.ingestChunk(makeTSChunk(4, true))
	time.Sleep(120 * time.Millisecond) // exceed segment duration

	// Second chunk with keyframe triggers split.
	seg.ingestChunk(makeTSChunk(4, true))

	seg.mu.RLock()
	segments := seg.recentSegments()
	seg.mu.RUnlock()

	if len(segments) == 0 {
		t.Fatal("expected at least 1 segment after keyframe split")
	}

	// Verify segment data length is exactly 4 packets (the first chunk).
	if len(segments[0].Data) != 4*mpegTSPacketSize {
		t.Errorf("segment data length = %d, want %d", len(segments[0].Data), 4*mpegTSPacketSize)
	}
}

func TestDiagnosticsSegmentProduction(t *testing.T) {
	seg, _ := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	// Before any segments, counters should be zero.
	d := seg.Diagnostics()
	if d.SegCount != 0 || d.SegSizeAvg != 0 || d.SecSinceSeg != 0 {
		t.Fatalf("expected zero diagnostics before segments, got %+v", d)
	}

	seg.touchPoll()
	time.Sleep(20 * time.Millisecond)

	// Produce two segments.
	seg.ingestChunk(makeTSChunk(4, true))
	time.Sleep(120 * time.Millisecond)
	seg.ingestChunk(makeTSChunk(8, true))
	time.Sleep(120 * time.Millisecond)
	seg.ingestChunk(makeTSChunk(4, true)) // triggers second segment

	d = seg.Diagnostics()
	if d.SegCount < 2 {
		t.Errorf("SegCount = %d, want >= 2", d.SegCount)
	}
	if d.SegSizeAvg == 0 {
		t.Error("SegSizeAvg should be > 0 after segments produced")
	}
	if d.SecSinceSeg <= 0 {
		t.Error("SecSinceSeg should be > 0 after segments produced")
	}
	if d.SegSizeMin == 0 {
		t.Error("SegSizeMin should be > 0 after segments produced")
	}
}

func TestDiagnosticsRequestCounters(t *testing.T) {
	seg, _ := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	seg.touchPoll()
	time.Sleep(20 * time.Millisecond)

	// Produce a segment so playlist and segment requests can succeed.
	seg.ingestChunk(makeTSChunk(4, true))
	time.Sleep(120 * time.Millisecond)
	seg.ingestChunk(makeTSChunk(4, true))

	// Serve a playlist.
	req := httptest.NewRequest("GET", "/live.m3u8?zip=90210", nil)
	rec := httptest.NewRecorder()
	seg.ServePlaylist(rec, req)

	d := seg.Diagnostics()
	if d.PlaylistReqs < 1 {
		t.Errorf("PlaylistReqs = %d, want >= 1", d.PlaylistReqs)
	}

	// Serve a segment.
	seg.mu.RLock()
	segments := seg.recentSegments()
	seg.mu.RUnlock()
	if len(segments) == 0 {
		t.Fatal("no segments for request counter test")
	}
	seqNum := segments[0].SeqNum
	req2 := httptest.NewRequest("GET", "/segment?zip=90210&seq="+strconv.Itoa(seqNum), nil)
	rec2 := httptest.NewRecorder()
	seg.ServeSegment(rec2, req2)

	d = seg.Diagnostics()
	if d.SegmentReqs < 1 {
		t.Errorf("SegmentReqs = %d, want >= 1", d.SegmentReqs)
	}

	// Request a non-existent segment.
	req3 := httptest.NewRequest("GET", "/segment?zip=90210&seq=99999", nil)
	rec3 := httptest.NewRecorder()
	seg.ServeSegment(rec3, req3)

	d = seg.Diagnostics()
	if d.SegmentMisses < 1 {
		t.Errorf("SegmentMisses = %d, want >= 1", d.SegmentMisses)
	}
}

func TestDiagnosticsLag(t *testing.T) {
	seg, _ := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	seg.touchPoll()
	time.Sleep(20 * time.Millisecond)

	// Produce several segments to create lag possibility.
	for i := 0; i < 6; i++ {
		seg.ingestChunk(makeTSChunk(4, true))
		time.Sleep(120 * time.Millisecond)
	}
	// One more to finalize the last.
	seg.ingestChunk(makeTSChunk(4, true))

	seg.mu.RLock()
	segments := seg.recentSegments()
	seg.mu.RUnlock()
	if len(segments) == 0 {
		t.Fatal("no segments for lag test")
	}

	// Request the oldest segment (should have lag > 0 if multiple segments exist).
	oldest := segments[0]
	req := httptest.NewRequest("GET", "/segment?zip=90210&seq="+strconv.Itoa(oldest.SeqNum), nil)
	rec := httptest.NewRecorder()
	seg.ServeSegment(rec, req)

	d := seg.Diagnostics()
	if d.LagMax < 0 {
		t.Errorf("LagMax = %d, want >= 0", d.LagMax)
	}
	// If we have multiple segments, requesting the oldest should produce lag.
	if len(segments) > 1 && d.LagMax == 0 {
		t.Errorf("LagMax = 0, expected > 0 when requesting oldest of %d segments", len(segments))
	}
}

func TestIngestChunkAccumulatesWithoutKeyframe(t *testing.T) {
	seg, _ := newTestSegmenter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go seg.Run(ctx)

	seg.touchPoll()
	time.Sleep(20 * time.Millisecond)

	// First chunk with keyframe.
	seg.ingestChunk(makeTSChunk(4, true))
	time.Sleep(120 * time.Millisecond)

	// Second chunk WITHOUT keyframe — should not finalize.
	seg.ingestChunk(makeTSChunk(4, false))

	seg.mu.RLock()
	segments := seg.recentSegments()
	seg.mu.RUnlock()

	if len(segments) != 0 {
		t.Error("should not finalize segment without keyframe in new data")
	}
}
