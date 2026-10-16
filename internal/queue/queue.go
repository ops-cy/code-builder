// Package queue schedules build jobs with a global concurrency cap while
// guaranteeing that jobs for the same repository never overlap.
package queue

import (
	"context"
	"errors"
	"sync"

	"gitlab-builder/internal/config"
)

// ErrQueueFull is returned by Enqueue when the pending list is at capacity.
var ErrQueueFull = errors.New("queue: full")

// ErrQueueClosed is returned by Enqueue after Start has shut the queue down.
var ErrQueueClosed = errors.New("queue: closed")

// Job is one queued build run.
type Job struct {
	BuildID string
	Repo    config.Repository
	Branch  string
	SHA     string
	Run     func(ctx context.Context) error
}

// Queue runs jobs with a global concurrency limit; jobs of the same
// Repository.Name never overlap and run FIFO per repository.
type Queue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	pending []Job
	running map[string]bool
	limit   int
	maxSize int
	closed  bool
	wg      sync.WaitGroup
}

// New creates a queue with the given worker count and pending capacity.
func New(concurrency, maxSize int) *Queue {
	if concurrency < 1 {
		concurrency = 1
	}
	if maxSize < 1 {
		maxSize = 1
	}
	q := &Queue{
		running: make(map[string]bool),
		limit:   concurrency,
		maxSize: maxSize,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue appends a job; it fails when the queue is full or closed.
func (q *Queue) Enqueue(j Job) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrQueueClosed
	}
	if len(q.pending) >= q.maxSize {
		return ErrQueueFull
	}
	q.pending = append(q.pending, j)
	q.cond.Signal()
	return nil
}

// Len returns the number of pending (not yet started) jobs.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Start launches workers and blocks until ctx is cancelled and all in-flight
// jobs have finished.
func (q *Queue) Start(ctx context.Context) {
	for i := 0; i < q.limit; i++ {
		q.wg.Add(1)
		go q.worker(ctx)
	}
	<-ctx.Done()
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
	q.wg.Wait()
}

// worker repeatedly picks the oldest pending job whose repository is free,
// runs it synchronously, then repeats until the queue closes.
func (q *Queue) worker(ctx context.Context) {
	defer q.wg.Done()
	for {
		q.mu.Lock()
		var job Job
		found := false
		for !q.closed && !found {
			if idx := pickLocked(q.pending, q.running); idx >= 0 {
				job = q.pending[idx]
				q.pending = append(q.pending[:idx], q.pending[idx+1:]...)
				q.running[job.Repo.Name] = true
				found = true
				continue
			}
			q.cond.Wait()
		}
		closed := q.closed
		q.mu.Unlock()
		if !found {
			if closed {
				return
			}
			continue
		}

		if ctx.Err() == nil {
			_ = job.Run(ctx)
		}

		q.mu.Lock()
		delete(q.running, job.Repo.Name)
		q.cond.Broadcast()
		q.mu.Unlock()
	}
}

// pickLocked returns the index of the oldest job whose repo is free, or -1.
func pickLocked(pending []Job, running map[string]bool) int {
	for i, j := range pending {
		if !running[j.Repo.Name] {
			return i
		}
	}
	return -1
}
