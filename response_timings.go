package main

import (
	"crypto/tls"
	"net/http/httptrace"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var timingTimelineSequence atomic.Uint64

// ResponseTimings marks observed phases explicitly; zero without its matching
// availability flag means the phase was not applicable or not exposed by Go.
type ResponseTimings struct {
	DNSMs             int64 `json:"dnsMs"`
	ConnectMs         int64 `json:"connectMs"`
	TLSMs             int64 `json:"tlsMs"`
	UploadMs          int64 `json:"uploadMs"`
	WaitMs            int64 `json:"waitMs"`
	DownloadMs        int64 `json:"downloadMs"`
	TotalMs           int64 `json:"totalMs"`
	RedirectCount     int   `json:"redirectCount"`
	ConnectionReused  bool  `json:"connectionReused"`
	DNSAvailable      bool  `json:"dnsAvailable"`
	ConnectAvailable  bool  `json:"connectAvailable"`
	TLSAvailable      bool  `json:"tlsAvailable"`
	UploadAvailable   bool  `json:"uploadAvailable"`
	WaitAvailable     bool  `json:"waitAvailable"`
	DownloadAvailable bool  `json:"downloadAvailable"`
}
type responseTimingTrace struct {
	mu                                                                     sync.Mutex
	start, dnsStart, connectStart, tlsStart, connAt, writeStart, firstByte time.Time
	timings                                                                ResponseTimings
}

func newResponseTimingTrace(start time.Time) *responseTimingTrace {
	return &responseTimingTrace{start: start}
}
func (t *responseTimingTrace) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { t.mu.Lock(); t.dnsStart = time.Now(); t.mu.Unlock() }, DNSDone: func(httptrace.DNSDoneInfo) {
			t.mu.Lock()
			if !t.dnsStart.IsZero() {
				t.timings.DNSAvailable = true
				t.timings.DNSMs += time.Since(t.dnsStart).Milliseconds()
			}
			t.mu.Unlock()
		},
		ConnectStart: func(_, _ string) { t.mu.Lock(); t.connectStart = time.Now(); t.mu.Unlock() }, ConnectDone: func(_, _ string, _ error) {
			t.mu.Lock()
			if !t.connectStart.IsZero() {
				t.timings.ConnectAvailable = true
				t.timings.ConnectMs += time.Since(t.connectStart).Milliseconds()
			}
			t.mu.Unlock()
		},
		TLSHandshakeStart: func() { t.mu.Lock(); t.tlsStart = time.Now(); t.mu.Unlock() }, TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			t.mu.Lock()
			if !t.tlsStart.IsZero() {
				t.timings.TLSAvailable = true
				t.timings.TLSMs += time.Since(t.tlsStart).Milliseconds()
			}
			t.mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			t.connAt = time.Now()
			t.timings.ConnectionReused = t.timings.ConnectionReused || info.Reused
			t.mu.Unlock()
		}, WroteRequest: func(httptrace.WroteRequestInfo) {
			t.mu.Lock()
			t.writeStart = time.Now()
			t.timings.UploadAvailable = true
			if !t.connAt.IsZero() {
				t.timings.UploadMs += t.writeStart.Sub(t.connAt).Milliseconds()
			}
			t.mu.Unlock()
		}, GotFirstResponseByte: func() {
			t.mu.Lock()
			t.firstByte = time.Now()
			t.timings.WaitAvailable = true
			if !t.writeStart.IsZero() {
				t.timings.WaitMs += t.firstByte.Sub(t.writeStart).Milliseconds()
			}
			t.mu.Unlock()
		},
	}
}
func (t *responseTimingTrace) redirect() { t.mu.Lock(); t.timings.RedirectCount++; t.mu.Unlock() }
func (t *responseTimingTrace) finalize(end time.Time) ResponseTimings {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.timings
	out.TotalMs = end.Sub(t.start).Milliseconds()
	if !t.firstByte.IsZero() {
		out.DownloadAvailable = true
		out.DownloadMs = end.Sub(t.firstByte).Milliseconds()
	}
	return out
}

func httpTimingTimelineItems(item RequestItem, response Response) []TimelineItem {
	t := response.Timings
	rows := []struct {
		name      string
		available bool
		ms        int64
	}{{"dns", t.DNSAvailable, t.DNSMs}, {"connect", t.ConnectAvailable, t.ConnectMs}, {"tls", t.TLSAvailable, t.TLSMs}, {"upload", t.UploadAvailable, t.UploadMs}, {"wait", t.WaitAvailable, t.WaitMs}, {"download", t.DownloadAvailable, t.DownloadMs}}
	out := []TimelineItem{}
	for _, row := range rows {
		if row.available {
			out = append(out, TimelineItem{ID: nextTimingTimelineID(item.ID, row.name), Kind: "network", Message: row.name, At: time.Now(), Duration: row.ms, RequestID: item.ID, Source: "network", Phase: row.name, Status: response.Status, StatusText: response.StatusText})
		}
	}
	if t.RedirectCount > 0 {
		out = append(out, TimelineItem{ID: nextTimingTimelineID(item.ID, "redirect"), Kind: "network", Message: "redirect", At: time.Now(), Duration: int64(t.RedirectCount), RequestID: item.ID, Source: "network", Phase: "redirect", Status: response.Status, StatusText: response.StatusText})
	}
	if t.ConnectionReused {
		out = append(out, TimelineItem{ID: nextTimingTimelineID(item.ID, "connection-reused"), Kind: "network", Message: "connection reused", At: time.Now(), RequestID: item.ID, Source: "network", Phase: "connection-reused", Status: response.Status, StatusText: response.StatusText})
	}
	return out
}

func nextTimingTimelineID(requestID, phase string) string {
	return "timeline-network-" + requestID + "-" + phase + "-" + strconv.FormatUint(timingTimelineSequence.Add(1), 10)
}
