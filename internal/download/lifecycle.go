package download

import (
	"context"
	"log"

	"github.com/uhhhm/reverb/internal/core"
)

// A Download's lifecycle:
//
//	queued ──start──▶ running ──output──▶ running+completion pending ──recorded──▶ completed
//	  │                 │  └─fails, times out──▶ failed
//	  │                 └─canceled──▶ canceled   (shutdown: back to queued)
//	  └─canceled──▶ canceled
//	failed, canceled ──Retry──▶ queued (a new attempt)
//	completed, failed, canceled ──Clear──▶ removed
//
// Every change is decided by transition, under transitionMu, against the row
// as it is stored now: work that read the job earlier, a dispatch left on the
// worker channel, a submission or poll that returns late, a progress sample or
// an automatic retry timer, carries the attempt it belongs to and changes
// nothing once that attempt has been canceled, superseded or cleared. A
// transition is published only after it has been persisted, so a client never
// sees a state the job list then contradicts.
//
// Completion is owned by completeOutput (see manager.go): once a downloader
// has produced output, that output is kept and recorded, and cancellation no
// longer applies to it.

// publication is the event a transition announces once it is persisted. An
// empty topic publishes nothing.
type publication struct {
	topic string
	msg   string
}

// transition applies decide to the job as currently stored and persists the
// result. decide reports false to leave the job alone. It returns the job as
// stored afterwards and whether the change was made.
func (m *Manager) transition(ctx context.Context, id string, decide func(*core.DownloadJob) (publication, bool)) (core.DownloadJob, bool, error) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	return m.transitionLocked(ctx, id, decide)
}

// transitionLocked is transition for a caller already holding transitionMu.
func (m *Manager) transitionLocked(ctx context.Context, id string, decide func(*core.DownloadJob) (publication, bool)) (core.DownloadJob, bool, error) {
	cur, ok, err := m.store.Get(ctx, id)
	if err != nil || !ok {
		return cur, false, err
	}
	next := cur
	pub, apply := decide(&next)
	if !apply {
		return cur, false, nil
	}
	m.stamp(&next)
	if err := m.store.Update(ctx, next); err != nil {
		return cur, false, err
	}
	if terminal(next.Status) {
		m.mu.Lock()
		delete(m.reqs, id)
		m.mu.Unlock()
	}
	if pub.topic != "" {
		m.publishEvent(pub.topic, next, pub.msg)
	}
	return next, true, nil
}

// stamp keeps a job's timestamps consistent with its status: a queued job has
// not started, a running one started when it first ran, and a finished one
// records when it finished.
func (m *Manager) stamp(j *core.DownloadJob) {
	now := m.clock.Now().Unix()
	switch {
	case j.Status == core.DownloadQueued:
		j.StartedAt, j.FinishedAt = 0, 0
	case j.Status == core.DownloadRunning:
		if j.StartedAt == 0 {
			j.StartedAt = now
		}
		j.FinishedAt = 0
	case terminal(j.Status):
		if j.FinishedAt == 0 {
			j.FinishedAt = now
		}
	}
}

func terminal(s core.DownloadStatus) bool {
	return s == core.DownloadCompleted || s == core.DownloadFailed || s == core.DownloadCanceled
}

// runningAttempt reports whether j is still the given attempt, running and
// not yet handed to completion.
func runningAttempt(j core.DownloadJob, attempt int) bool {
	return j.Status == core.DownloadRunning && !j.CompletionPending && j.Attempts == attempt
}

// fail ends a running attempt as failed.
func (m *Manager) fail(ctx context.Context, id string, attempt int, reason string) (core.DownloadJob, bool) {
	job, ok, err := m.transition(ctx, id, func(j *core.DownloadJob) (publication, bool) {
		if !runningAttempt(*j, attempt) {
			return publication{}, false
		}
		j.Status, j.Error, j.FinishedAt = core.DownloadFailed, reason, 0
		return publication{TopicFailed, reason}, true
	})
	m.logTransitionError(id, "fail", err)
	return job, ok
}

// progress records a sample for an attempt that is still running. A sample is
// not a transition: it is written conditionally rather than under
// transitionMu, so a slow external poll holding that lock never stalls a
// downloader's output.
func (m *Manager) progress(ctx context.Context, id string, attempt, p int) {
	ok, err := m.store.UpdateProgress(ctx, id, attempt, p)
	if err != nil || !ok {
		return
	}
	if job, found, err := m.store.Get(ctx, id); err == nil && found && runningAttempt(job, attempt) {
		m.publishEvent(TopicProgress, job, "")
	}
}

func (m *Manager) logTransitionError(id, what string, err error) {
	if err != nil {
		log.Printf("download: %s job %s: %v", what, shortID(id), err)
	}
}
