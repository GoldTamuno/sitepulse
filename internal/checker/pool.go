package checker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/yourname/sitepulse/internal/domain"
)

// Job is a unit of work submitted to the pool: "check this monitor."
type Job struct {
	Monitor *domain.Monitor
}

// WorkerPool runs a fixed number of goroutines that pull Jobs off a
// channel and produce Results on another channel. This is the classic Go
// worker pool pattern, and the reason it exists rather than just doing
// `go RunCheck(...)` for every due monitor is capacity control: with, say,
// 500 active monitors all due at once, spawning 500 simultaneous
// goroutines each opening an outbound HTTP connection could exhaust file
// descriptors, saturate outbound bandwidth, or overwhelm the machine —
// none of which makes the checks run any faster, it just makes everything
// worse at once. A bounded pool of N workers processes the same 500 jobs
// through a fixed-width pipe, trading a bit of queueing latency for
// predictable resource usage.
type WorkerPool struct {
	jobs    chan Job
	results chan *domain.Check

	workerCount   int
	slowThreshold time.Duration
	log           *slog.Logger

	wg sync.WaitGroup
}

// NewWorkerPool constructs a pool. jobs is buffered modestly (2x worker
// count) so the scheduler's Submit doesn't have to block-and-wait for a
// free worker on every single monitor during a tick with many due
// monitors at once — but it's still bounded, not unbounded, so a huge
// backlog still applies backpressure to the scheduler rather than growing
// memory without limit.
func NewWorkerPool(workerCount int, slowThreshold time.Duration, log *slog.Logger) *WorkerPool {
	return &WorkerPool{
		jobs:          make(chan Job, workerCount*2),
		results:       make(chan *domain.Check, workerCount*2),
		workerCount:   workerCount,
		slowThreshold: slowThreshold,
		log:           log,
	}
}

// Start spawns the worker goroutines. Each worker loops on the jobs
// channel until it's closed (via Shutdown), at which point `range`
// naturally exits and the worker's WaitGroup entry completes. This is the
// standard "close the channel to signal done, range to consume until
// closed" idiom — it's what lets Shutdown guarantee every in-flight job
// finishes before returning, without any explicit "are you done yet?"
// polling.
func (p *WorkerPool) Start(ctx context.Context) {
	for i := 0; i < p.workerCount; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}
}

func (p *WorkerPool) worker(ctx context.Context, id int) {
	defer p.wg.Done()

	for job := range p.jobs {
		check := RunCheck(ctx, job.Monitor, p.slowThreshold)

		p.log.Debug("check completed",
			"worker_id", id,
			"monitor_id", job.Monitor.ID,
			"status", check.Status,
			"status_code", check.StatusCode,
			"response_time_ms", check.ResponseTime.Milliseconds(),
		)

		select {
		case p.results <- check:
		case <-ctx.Done():
			// Shutting down and nobody's draining results anymore — drop
			// this result rather than block forever trying to send it
			// into a channel nobody will ever read from again.
			return
		}
	}
}

// Submit hands a job to the pool. It uses a select against ctx.Done()
// rather than a bare channel send specifically to avoid deadlocking during
// shutdown: if the jobs channel is full (all workers busy, buffer full)
// and the context gets cancelled at the same moment, a bare `p.jobs <-
// job` would block forever since nothing will ever drain it again. The
// select gives Submit an escape hatch.
func (p *WorkerPool) Submit(ctx context.Context, job Job) {
	select {
	case p.jobs <- job:
	case <-ctx.Done():
	}
}

// Results exposes a read-only channel of completed checks for a consumer
// (the result recorder) to range over. Read-only (<-chan) at the type
// level, not just by convention — the compiler enforces that nothing
// outside this package can accidentally send into or close this channel
// from the wrong place.
func (p *WorkerPool) Results() <-chan *domain.Check {
	return p.results
}

// Shutdown closes the jobs channel (no more work will be accepted), waits
// for every in-flight check to actually finish via the WaitGroup, then
// closes the results channel — which is what lets a `range` loop over
// Results() terminate cleanly instead of hanging forever waiting for a
// channel that will never receive again but also never gets closed.
//
// Order matters here: closing jobs before waiting is what makes workers'
// `for job := range p.jobs` loops actually exit; waiting before closing
// results is what guarantees no worker is still trying to send into
// results after we've closed it (sending on a closed channel panics).
func (p *WorkerPool) Shutdown() {
	close(p.jobs)
	p.wg.Wait()
	close(p.results)
}
