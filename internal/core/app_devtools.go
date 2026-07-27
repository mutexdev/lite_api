package core

// The DevTools snapshot the panel polls, and the CPU sampling behind it.
//
// Split out of app.go by AST: declarations are identified by the parser
// and copied verbatim from their source offsets.

import (
	"math"
	"os"
	goruntime "runtime"
	"time"
)

func (a *App) GetDevToolsSnapshot() (DevToolsSnapshot, error) {
	a.mu.Lock()
	if err := a.ensureReadyLocked(); err != nil {
		a.mu.Unlock()
		return DevToolsSnapshot{}, err
	}
	startedAt := a.startedAt
	networkRequests := len(a.state.NetworkLog)
	consoleLogs := countScriptLogsInState(a.state)
	a.mu.Unlock()

	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	var mem goruntime.MemStats
	goruntime.ReadMemStats(&mem)
	uptimeSeconds := int64(time.Since(startedAt).Seconds())
	cpuPercent := a.sampleDevToolsCPU(time.Now())
	return DevToolsSnapshot{
		PID:             os.Getpid(),
		CPUPercent:      cpuPercent,
		UptimeSeconds:   uptimeSeconds,
		MemoryBytes:     mem.Sys,
		HeapAllocBytes:  mem.HeapAlloc,
		Goroutines:      goruntime.NumGoroutine(),
		NetworkRequests: networkRequests,
		ConsoleLogs:     consoleLogs,
		Processes: []DevToolsProcessMetric{
			{
				PID:           os.Getpid(),
				Title:         "LiteAPI",
				Type:          "main",
				CPUPercent:    cpuPercent,
				MemoryBytes:   mem.Sys,
				UptimeSeconds: uptimeSeconds,
			},
		},
		Timestamp: time.Now(),
	}, nil
}

func (a *App) sampleDevToolsCPU(now time.Time) float64 {
	cpuTime, ok := currentProcessCPUTime()
	if !ok {
		return 0
	}
	a.cpuMu.Lock()
	defer a.cpuMu.Unlock()
	percent := calculateCPUPercent(a.lastCPUTime, a.lastCPUWall, cpuTime, now, goruntime.NumCPU())
	a.lastCPUTime = cpuTime
	a.lastCPUWall = now
	return percent
}

func calculateCPUPercent(previousCPU time.Duration, previousWall time.Time, currentCPU time.Duration, currentWall time.Time, cpuCount int) float64 {
	if currentCPU < 0 || cpuCount <= 0 {
		return 0
	}
	if previousWall.IsZero() || previousCPU <= 0 || !currentWall.After(previousWall) || currentCPU < previousCPU {
		return 0
	}
	wallDelta := currentWall.Sub(previousWall)
	if wallDelta <= 0 {
		return 0
	}
	percent := (float64(currentCPU-previousCPU) / float64(wallDelta)) * 100 / float64(cpuCount)
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 {
		return 0
	}
	return math.Round(percent*10) / 10
}

func countScriptLogsInState(state AppState) int {
	total := 0
	for _, workspace := range state.Workspaces {
		for _, collection := range workspace.Collections {
			for _, item := range collection.Items {
				if item.Response != nil {
					total += len(item.Response.ScriptLogs)
				}
			}
		}
	}
	return total
}
