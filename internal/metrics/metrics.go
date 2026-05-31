// Package metrics samples /proc to produce CPU, memory, and goroutine
// counters for the dashboard.
//
// Every reader returns zero values on parse failure rather than an error.
// On Android, /proc layouts vary across kernel versions and Magisk's own
// mounts can occasionally hide files; the dashboard must keep rendering
// even when one specific file is unreadable.
package metrics

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Snapshot holds a point-in-time set of process and system metrics.
type Snapshot struct {
	CPUPercent       float64
	MemoryRSSBytes   uint64
	MemoryRSSMB      float64
	SystemMemTotal   uint64
	SystemMemAvail   uint64
	SystemMemUsedPct float64
	Goroutines       int
}

// Sampler keeps the last CPU tick counters between calls. CPU% is only
// meaningful as a delta over time, so the first call always returns 0%.
type Sampler struct {
	lastSysTicks  uint64
	lastProcTicks uint64
}

// Sample reads /proc once and returns the current snapshot.
func (s *Sampler) Sample() Snapshot {
	out := Snapshot{}
	sys, okSys := readSystemCPUTicks()
	proc, okProc := readProcessCPUTicks()
	if okSys && okProc && s.lastSysTicks > 0 && sys > s.lastSysTicks {
		dt := sys - s.lastSysTicks
		dp := proc - s.lastProcTicks
		out.CPUPercent = round1((float64(dp) / float64(dt)) * 100.0)
	}
	if okSys {
		s.lastSysTicks = sys
	}
	if okProc {
		s.lastProcTicks = proc
	}
	rss := readRSSBytes()
	out.MemoryRSSBytes = rss
	out.MemoryRSSMB = round1(float64(rss) / 1024.0 / 1024.0)
	total, avail := readMemInfoBytes()
	out.SystemMemTotal = total
	out.SystemMemAvail = avail
	if total > 0 && avail <= total {
		out.SystemMemUsedPct = round1((float64(total-avail) / float64(total)) * 100.0)
	}
	out.Goroutines = runtime.NumGoroutine()
	return out
}

// round1 returns v rounded to one decimal place, never negative. CPU and
// memory percentages are always reported clamped at 0 because a tiny
// timing skew can produce small negative deltas that look ugly in the UI.
func round1(v float64) float64 {
	if v < 0 {
		v = 0
	}
	return float64(int(v*10+0.5)) / 10
}

// readSystemCPUTicks returns the sum of all CPU time fields on the first
// "cpu " line of /proc/stat.
func readSystemCPUTicks() (uint64, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "cpu ") {
		return 0, false
	}
	var total uint64
	for _, f := range strings.Fields(lines[0])[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err == nil {
			total += v
		}
	}
	return total, total > 0
}

// readProcessCPUTicks returns utime+stime (user+system jiffies) for the
// current process by parsing /proc/self/stat. The comm field can contain
// spaces or parentheses, so we anchor on the LAST ')' to find the start
// of the rest of the fields.
func readProcessCPUTicks() (uint64, bool) {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, false
	}
	s := string(b)
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx+2 >= len(s) {
		return 0, false
	}
	fields := strings.Fields(s[idx+2:])
	if len(fields) < 13 {
		return 0, false
	}
	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return utime + stime, true
}

// readRSSBytes returns the resident set size in bytes from /proc/self/statm.
// Field index 1 is RSS in pages.
func readRSSBytes() uint64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}

// readMemInfoBytes returns total and available memory in bytes by parsing
// /proc/meminfo. Returns (0, 0) on any read failure.
func readMemInfoBytes() (uint64, uint64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	var total, avail uint64
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = v * 1024
		case "MemAvailable":
			avail = v * 1024
		}
	}
	return total, avail
}
