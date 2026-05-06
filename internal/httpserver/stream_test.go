package httpserver

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// readSSEEvents reads SSE events off a response body until the predicate
// returns true or the context fires. Returns the events seen.
type sseEvent struct {
	name string
	data string
}

func readSSEEvents(t *testing.T, body *http.Response, deadline time.Duration, until func([]sseEvent) bool) []sseEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	events := []sseEvent{}
	scanner := bufio.NewScanner(body.Body)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)

	current := sseEvent{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				current.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				current.data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if current.name != "" {
					events = append(events, current)
					current = sseEvent{}
					if until(events) {
						return
					}
				}
			}
		}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
	return events
}

func TestStream_NotFound(t *testing.T) {
	_, h := newTestServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/jobs/does-not-exist/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestStream_HeadersAndInitialStatus(t *testing.T) {
	srvObj, h := newTestServer(t)
	seedJob(t, srvObj.db, "stream-1", "https://example.com", "running")
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/jobs/stream-1/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("expected SSE content type, got %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("expected no-cache, got %q", got)
	}

	// Read events until we get an initial status. Bail after 3s.
	events := readSSEEvents(t, resp, 3*time.Second, func(evs []sseEvent) bool {
		for _, e := range evs {
			if e.name == "status" {
				return true
			}
		}
		return false
	})

	hasStatus := false
	for _, e := range events {
		if e.name == "status" {
			hasStatus = true
			if !strings.Contains(e.data, `"jobId":"stream-1"`) {
				t.Errorf("status payload missing jobId: %s", e.data)
			}
			if !strings.Contains(e.data, `"status":"running"`) {
				t.Errorf("status payload missing status field: %s", e.data)
			}
		}
	}
	if !hasStatus {
		t.Fatalf("expected at least one `status` event within 3s, got events: %+v", events)
	}
}

func TestStream_TerminalSendsDoneAndCloses(t *testing.T) {
	srvObj, h := newTestServer(t)
	seedJob(t, srvObj.db, "stream-done", "https://example.com", "completed")
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/jobs/stream-done/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	// Read until we see a `done` event.
	events := readSSEEvents(t, resp, 4*time.Second, func(evs []sseEvent) bool {
		for _, e := range evs {
			if e.name == "done" {
				return true
			}
		}
		return false
	})

	gotDone := false
	for _, e := range events {
		if e.name == "done" {
			gotDone = true
			if !strings.Contains(e.data, `"status":"completed"`) {
				t.Errorf("done event missing status: %s", e.data)
			}
		}
	}
	if !gotDone {
		t.Fatalf("expected a `done` event for a completed job, events: %+v", events)
	}
}

func TestStream_ActivityIncludesPhaseEvents(t *testing.T) {
	srvObj, h := newTestServer(t)
	seedJob(t, srvObj.db, "stream-act", "https://example.com", "running")

	// Seed a phase event so the stream has something to deliver in the activity feed.
	details := `{"phase":"asset_checks","message":"HEAD-checking 5 assets"}`
	if _, err := srvObj.db.InsertEvent("stream-act", "phase", &details, nil); err != nil {
		t.Fatalf("seeding event: %v", err)
	}

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/jobs/stream-act/stream")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	events := readSSEEvents(t, resp, 3*time.Second, func(evs []sseEvent) bool {
		for _, e := range evs {
			if e.name == "activity" {
				return true
			}
		}
		return false
	})

	gotActivity := false
	for _, e := range events {
		if e.name == "activity" && strings.Contains(e.data, `"kind":"phase"`) {
			gotActivity = true
			if !strings.Contains(e.data, `"phase":"asset_checks"`) {
				t.Errorf("phase activity missing phase field: %s", e.data)
			}
		}
	}
	if !gotActivity {
		t.Fatalf("expected a phase `activity` event, got: %+v", events)
	}
}
