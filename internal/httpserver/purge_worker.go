package httpserver

import (
	"log/slog"
	"sync"

	"github.com/ggonzalezaleman/seo-crawler-mcp/internal/storage"
)

// purgeWorker runs job purges sequentially on a single goroutine so two
// concurrent DELETE requests don't both try to grab the (single) SQLite write
// connection at the same time. The DELETE handler enqueues a jobID and returns
// 200 immediately; this worker drains the queue at its own pace.
//
// Lifetime: started on the first enqueue, stops never (process-lifetime).
// Lost jobs on a server restart are picked up by the orphan reaper, which
// also reaps anything left in 'deleting' status.
type purgeWorker struct {
	db    *storage.DB
	queue chan string
	once  sync.Once
}

func newPurgeWorker(db *storage.DB) *purgeWorker {
	return &purgeWorker{
		db: db,
		// Buffered so a flurry of clicks doesn't block the HTTP handler.
		// 64 is plenty: we don't expect a human to queue more than that.
		queue: make(chan string, 64),
	}
}

// enqueue schedules a job for purging. Non-blocking; if the queue is full, the
// purge is performed inline (rare; would only happen if 64 deletes are pending).
func (w *purgeWorker) enqueue(jobID string) {
	w.once.Do(func() { go w.run() })
	select {
	case w.queue <- jobID:
	default:
		// Queue full: do it inline. Slow but at least it gets done.
		slog.Warn("purge queue full, running inline", "job", jobID)
		if err := w.db.PurgeJob(jobID); err != nil {
			slog.Error("inline purge failed", "job", jobID, "err", err)
		}
	}
}

// EnqueuePurge is the public entry point used by main() to resume pending
// purges on startup. The DELETE handler uses (*purgeWorker).enqueue directly.
func (s *Server) EnqueuePurge(jobID string) {
	if s.purger != nil {
		s.purger.enqueue(jobID)
	}
}

func (w *purgeWorker) run() {
	for jobID := range w.queue {
		if err := w.db.PurgeJob(jobID); err != nil {
			slog.Error("purge failed", "job", jobID, "err", err)
			continue
		}
		slog.Info("purge complete", "job", jobID)
	}
}
