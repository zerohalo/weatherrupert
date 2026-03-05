// Package sysstat exposes host load average and container CPU usage
// by parsing /proc and cgroup files. All functions degrade gracefully
// on systems where these files don't exist (e.g. macOS).
package sysstat

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LoadAvg reads /proc/loadavg and returns the 1m, 5m, and 15m load averages.
// Returns zeros if the file doesn't exist (macOS, Windows).
func LoadAvg() (load1, load5, load15 float64, err error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, nil // graceful: not Linux
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("sysstat: unexpected /proc/loadavg format")
	}
	load1, _ = strconv.ParseFloat(fields[0], 64)
	load5, _ = strconv.ParseFloat(fields[1], 64)
	load15, _ = strconv.ParseFloat(fields[2], 64)
	return load1, load5, load15, nil
}

// CPUSampler periodically reads container CPU time from cgroup v2/v1
// and computes a usage percentage between samples. The percentage is
// relative to the container's allocated CPU cores (e.g. cpus: "2.0"
// fully used = 100%), falling back to host core count if no limit is set.
type CPUSampler struct {
	mu      sync.Mutex
	pct     float64 // latest CPU percentage, or -1 if unavailable
	cores   float64 // allocated CPU cores (from cgroup quota or host)
	stop    chan struct{}
	stopped chan struct{}
}

// NewCPUSampler starts a background goroutine that samples container CPU
// every 5 seconds. Call Stop() to clean up.
func NewCPUSampler() *CPUSampler {
	s := &CPUSampler{
		pct:     -1,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go s.run()
	return s
}

// Usage returns the latest container CPU usage as a percentage of allocated
// cores (e.g. 45.2 for 45.2%). Returns -1 if cgroup files are not available.
func (s *CPUSampler) Usage() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pct
}

// Cores returns the number of CPU cores allocated to the container.
func (s *CPUSampler) Cores() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cores
}

// Stop terminates the background sampling goroutine.
func (s *CPUSampler) Stop() {
	close(s.stop)
	<-s.stopped
}

func (s *CPUSampler) run() {
	defer close(s.stopped)

	prevUsec, ok := readCPUUsec()
	if !ok {
		return // no cgroup CPU stats available; pct stays -1
	}
	prevTime := time.Now()

	cores := readCPUCores()
	s.mu.Lock()
	s.cores = cores
	s.mu.Unlock()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			curUsec, ok := readCPUUsec()
			if !ok {
				continue
			}
			now := time.Now()
			deltaWall := now.Sub(prevTime).Microseconds()
			if deltaWall <= 0 {
				continue
			}
			deltaCPU := curUsec - prevUsec
			// Normalize to allocated cores: 100% = all allocated cores fully used.
			pct := float64(deltaCPU) / float64(deltaWall) / cores * 100.0

			s.mu.Lock()
			s.pct = pct
			s.mu.Unlock()

			prevUsec = curUsec
			prevTime = now
		case <-s.stop:
			return
		}
	}
}

// readCPUUsec returns container CPU usage in microseconds.
// It tries cgroup v2 first, then falls back to cgroup v1.
func readCPUUsec() (int64, bool) {
	// cgroup v2: /sys/fs/cgroup/cpu.stat contains "usage_usec <N>"
	if data, err := os.ReadFile("/sys/fs/cgroup/cpu.stat"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "usage_usec ") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if v, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
						return v, true
					}
				}
			}
		}
	}

	// cgroup v1: /sys/fs/cgroup/cpuacct/cpuacct.usage contains nanoseconds
	if data, err := os.ReadFile("/sys/fs/cgroup/cpuacct/cpuacct.usage"); err == nil {
		s := strings.TrimSpace(string(data))
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v / 1000, true // convert ns → µs
		}
	}

	return 0, false
}

// readCPUCores returns the number of CPU cores allocated to the container.
// It reads the cgroup v2 quota first, then falls back to v1, then to the
// host's total core count if no limit is configured.
func readCPUCores() float64 {
	// cgroup v2: /sys/fs/cgroup/cpu.max contains "quota period" (e.g. "200000 100000" = 2.0 cores)
	// or "max 100000" if unlimited.
	if data, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		fields := strings.Fields(strings.TrimSpace(string(data)))
		if len(fields) >= 2 && fields[0] != "max" {
			quota, err1 := strconv.ParseFloat(fields[0], 64)
			period, err2 := strconv.ParseFloat(fields[1], 64)
			if err1 == nil && err2 == nil && period > 0 {
				return quota / period
			}
		}
	}

	// cgroup v1: /sys/fs/cgroup/cpu/cpu.cfs_quota_us and cpu.cfs_period_us
	// quota = -1 means unlimited.
	if qData, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us"); err == nil {
		if pData, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us"); err == nil {
			quota, err1 := strconv.ParseFloat(strings.TrimSpace(string(qData)), 64)
			period, err2 := strconv.ParseFloat(strings.TrimSpace(string(pData)), 64)
			if err1 == nil && err2 == nil && quota > 0 && period > 0 {
				return quota / period
			}
		}
	}

	// No cgroup limit — fall back to host core count.
	return float64(runtime.NumCPU())
}
