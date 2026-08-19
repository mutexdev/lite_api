// DevTools snapshots and process metrics.
//
// US-060. Moved verbatim from app.go; see internal/types/proxy.go for why the
// aliases left behind in internal/core are a Go shim and not a Wails one.
package types

import "time"

type DevToolsSnapshot struct {
	PID             int                     `json:"pid"`
	CPUPercent      float64                 `json:"cpuPercent"`
	UptimeSeconds   int64                   `json:"uptimeSeconds"`
	MemoryBytes     uint64                  `json:"memoryBytes"`
	HeapAllocBytes  uint64                  `json:"heapAllocBytes"`
	Goroutines      int                     `json:"goroutines"`
	NetworkRequests int                     `json:"networkRequests"`
	ConsoleLogs     int                     `json:"consoleLogs"`
	Processes       []DevToolsProcessMetric `json:"processes"`
	Timestamp       time.Time               `json:"timestamp"`
}

type DevToolsProcessMetric struct {
	PID           int     `json:"pid"`
	Title         string  `json:"title"`
	Type          string  `json:"type"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryBytes   uint64  `json:"memoryBytes"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
}
