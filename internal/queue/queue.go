// Package queue 在全局并发上限下调度构建任务，同时保证同一仓库的
// 任务永远不会重叠执行。
package queue

import (
	"context"
	"errors"
	"sync"

	"gitlab-builder/internal/config"
)

// ErrQueueFull 在待处理列表已满时由 Enqueue 返回。
var ErrQueueFull = errors.New("queue: full")

// ErrQueueClosed 在 Start 关闭队列后由 Enqueue 返回。
var ErrQueueClosed = errors.New("queue: closed")

// Job 是一次排队的构建运行。
type Job struct {
	BuildID string
	Repo    config.Repository
	Branch  string
	SHA     string
	Run     func(ctx context.Context) error
}

// Queue 在全局并发上限下运行任务；同一仓库（Repo.Key，即规范化后的
// clone URL）的任务不会重叠，并按仓库以先进先出顺序执行。
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

// New 创建队列，指定 worker 数量和待处理容量。
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

// Enqueue 追加一个任务；队列已满或已关闭时返回错误。
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

// Len 返回待处理（尚未开始）的任务数量。
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Start 启动 worker，并阻塞直到 ctx 被取消且所有进行中的任务结束。
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

// worker 反复挑选仓库空闲的最早待处理任务，同步执行，然后重复，
// 直到队列关闭。
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
				q.running[repoKey(job)] = true
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
		delete(q.running, repoKey(job))
		q.cond.Broadcast()
		q.mu.Unlock()
	}
}

// repoKey 是串行化标识：仓库名（URL 的最后一段路径），因此推送到同名
// 仓库的任务不会重叠。对于没有名称的测试数据，回退到规范化后的 clone URL。
func repoKey(j Job) string {
	if j.Repo.Name != "" {
		return j.Repo.Name
	}
	return j.Repo.Key
}

// pickLocked 返回仓库空闲的最早任务的索引，没有则返回 -1。
func pickLocked(pending []Job, running map[string]bool) int {
	for i, j := range pending {
		if !running[repoKey(j)] {
			return i
		}
	}
	return -1
}
