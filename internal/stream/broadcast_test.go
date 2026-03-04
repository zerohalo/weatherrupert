package stream

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHubSubscribeUnsubscribe(t *testing.T) {
	hub := NewHub()

	ch := hub.Subscribe()
	if hub.ClientCount() != 1 {
		t.Errorf("ClientCount = %d, want 1", hub.ClientCount())
	}

	hub.Unsubscribe(ch)
	if hub.ClientCount() != 0 {
		t.Errorf("ClientCount = %d after unsubscribe, want 0", hub.ClientCount())
	}
}

func TestHubBroadcast(t *testing.T) {
	hub := NewHub()
	ch1 := hub.Subscribe()
	ch2 := hub.Subscribe()
	defer hub.Unsubscribe(ch1)
	defer hub.Unsubscribe(ch2)

	data := []byte("hello")
	hub.broadcast(data)

	got1 := <-ch1
	got2 := <-ch2
	if !bytes.Equal(got1, data) {
		t.Errorf("ch1 got %q, want %q", got1, data)
	}
	if !bytes.Equal(got2, data) {
		t.Errorf("ch2 got %q, want %q", got2, data)
	}
}

func TestHubOnActiveOnIdle(t *testing.T) {
	hub := NewHub()

	var activeCalls, idleCalls atomic.Int32
	hub.OnActive = func() { activeCalls.Add(1) }
	hub.OnIdle = func() { idleCalls.Add(1) }

	// First subscribe triggers OnActive.
	ch1 := hub.Subscribe()
	if activeCalls.Load() != 1 {
		t.Errorf("OnActive called %d times, want 1", activeCalls.Load())
	}

	// Second subscribe does not trigger OnActive again.
	ch2 := hub.Subscribe()
	if activeCalls.Load() != 1 {
		t.Errorf("OnActive called %d times after second subscribe, want 1", activeCalls.Load())
	}

	// First unsubscribe does not trigger OnIdle (still one client).
	hub.Unsubscribe(ch2)
	if idleCalls.Load() != 0 {
		t.Errorf("OnIdle called %d times, want 0", idleCalls.Load())
	}

	// Last unsubscribe triggers OnIdle.
	hub.Unsubscribe(ch1)
	if idleCalls.Load() != 1 {
		t.Errorf("OnIdle called %d times after last unsubscribe, want 1", idleCalls.Load())
	}
}

func TestHubTotalViews(t *testing.T) {
	hub := NewHub()

	ch1 := hub.Subscribe()
	ch2 := hub.Subscribe()
	hub.Unsubscribe(ch1)
	hub.Unsubscribe(ch2)

	if hub.TotalViews() != 2 {
		t.Errorf("TotalViews = %d, want 2", hub.TotalViews())
	}
}

func TestHubTotalViewTime(t *testing.T) {
	hub := NewHub()

	ch := hub.Subscribe()
	time.Sleep(50 * time.Millisecond)
	hub.Unsubscribe(ch)

	vt := hub.TotalViewTime()
	if vt < 40*time.Millisecond {
		t.Errorf("TotalViewTime = %v, expected >= 40ms", vt)
	}
}

// TestHubClientDropsRace exercises concurrent broadcast and ClientDrops calls
// to verify the atomic.Int64 drop counter doesn't race. Run with -race.
func TestHubClientDropsRace(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	// Fill the buffer so subsequent broadcasts cause drops.
	for i := 0; i < clientBufLen; i++ {
		hub.broadcast([]byte("fill"))
	}

	var wg sync.WaitGroup
	// Goroutine 1: broadcast (causes drops via atomic Add).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			hub.broadcast([]byte("drop"))
		}
	}()
	// Goroutine 2: read drop count concurrently (via atomic Load).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			hub.ClientDrops()
		}
	}()
	wg.Wait()

	if drops := hub.ClientDrops(); drops < 500 {
		t.Errorf("ClientDrops = %d, want >= 500", drops)
	}
}

func TestHubDiagnosticsCounters(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	hub.broadcast([]byte("hello"))
	hub.broadcast([]byte("world!"))
	// Drain so channel doesn't fill.
	<-ch
	<-ch

	diag := hub.Diagnostics()
	if diag.ChunkCount != 2 {
		t.Errorf("ChunkCount = %d, want 2", diag.ChunkCount)
	}
	if diag.BytesTotal != 11 {
		t.Errorf("BytesTotal = %d, want 11", diag.BytesTotal)
	}
	if diag.SecSinceSend < 0 || diag.SecSinceSend > 1 {
		t.Errorf("SecSinceSend = %f, want 0..1", diag.SecSinceSend)
	}
	if diag.KBps <= 0 {
		t.Errorf("KBps = %f, want > 0", diag.KBps)
	}
}

func TestHubDiagnosticsZeroBeforeBroadcast(t *testing.T) {
	hub := NewHub()
	diag := hub.Diagnostics()
	if diag.ChunkCount != 0 {
		t.Errorf("ChunkCount = %d, want 0", diag.ChunkCount)
	}
	if diag.BytesTotal != 0 {
		t.Errorf("BytesTotal = %d, want 0", diag.BytesTotal)
	}
	if diag.SecSinceSend != 0 {
		t.Errorf("SecSinceSend = %f, want 0", diag.SecSinceSend)
	}
	if diag.KBps != 0 {
		t.Errorf("KBps = %f, want 0", diag.KBps)
	}
}

func TestHubSlowClientDrop(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	// Fill the client buffer.
	for i := 0; i < clientBufLen; i++ {
		hub.broadcast([]byte("x"))
	}

	// Next broadcast should be dropped (not block).
	done := make(chan struct{})
	go func() {
		hub.broadcast([]byte("overflow"))
		close(done)
	}()

	select {
	case <-done:
		// Good — broadcast didn't block.
	case <-time.After(1 * time.Second):
		t.Error("broadcast blocked on slow client")
	}
}

func TestHubRunReadsToEOF(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	data := bytes.Repeat([]byte("A"), chunkSize*2+100)
	r := bytes.NewReader(data)

	done := make(chan struct{})
	go func() {
		hub.Run(r)
		close(done)
	}()

	// Drain the client channel until Run finishes.
	var total int
	for {
		select {
		case chunk := <-ch:
			total += len(chunk)
		case <-done:
			// Drain remaining.
			for {
				select {
				case chunk := <-ch:
					total += len(chunk)
				default:
					if total != len(data) {
						t.Errorf("received %d bytes, want %d", total, len(data))
					}
					return
				}
			}
		}
	}
}
