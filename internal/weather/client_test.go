package weather

import (
	"fmt"
	"sync"
	"testing"
)

// TestLocationRace exercises concurrent writes to location (simulating bootstrap)
// and reads via Location() (simulating the admin dashboard) to verify the
// locationMu fix prevents data races. Run with -race.
func TestLocationRace(t *testing.T) {
	c := NewClient("http://localhost", 40.0, -105.0, "Initial, CO", 4, 120, nil, nil, nil)

	var wg sync.WaitGroup
	// Goroutine 1: simulate bootstrap writing location.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			c.locationMu.Lock()
			c.location = fmt.Sprintf("City %d, ST", i)
			c.locationMu.Unlock()
		}
	}()
	// Goroutine 2: read Location() concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_ = c.Location()
		}
	}()
	wg.Wait()

	// After all writes, Location should return the last value written.
	if got := c.Location(); got != "City 499, ST" {
		t.Errorf("Location() = %q, want %q", got, "City 499, ST")
	}
}
