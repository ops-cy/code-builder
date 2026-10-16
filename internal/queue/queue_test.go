package queue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gitlab-builder/internal/config"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func repoNamed(name string) config.Repository {
	return config.Repository{Name: name}
}

// startQueue runs q until ctx cancel; returns a done channel closed on exit.
func startQueue(t *testing.T, q *Queue) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		q.Start(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("queue did not shut down in time")
		}
	})
	return cancel
}

// tracker records per-repo concurrency and start order.
type tracker struct {
	mu        sync.Mutex
	active    map[string]int
	maxSeen   map[string]int
	global    int
	maxGlobal int
	order     []string
	started   chan string
}

func newTracker() *tracker {
	return &tracker{
		active:  make(map[string]int),
		maxSeen: make(map[string]int),
		started: make(chan string, 64),
	}
}

func (tr *tracker) run(repo, id string, hold func()) func(context.Context) error {
	return func(ctx context.Context) error {
		tr.mu.Lock()
		tr.active[repo]++
		tr.global++
		if tr.active[repo] > tr.maxSeen[repo] {
			tr.maxSeen[repo] = tr.active[repo]
		}
		if tr.global > tr.maxGlobal {
			tr.maxGlobal = tr.global
		}
		tr.order = append(tr.order, id)
		tr.mu.Unlock()
		tr.started <- id
		if hold != nil {
			hold()
		}
		tr.mu.Lock()
		tr.active[repo]--
		tr.global--
		tr.mu.Unlock()
		return nil
	}
}

func Test_Queue_when_TwoRepos_ThenRunInParallel(t *testing.T) {
	// Given
	q := New(2, 10)
	tr := newTracker()
	var arrived sync.WaitGroup
	arrived.Add(2)
	// Both jobs call Done then Wait: neither returns until both are in-flight.
	hold := func() {
		arrived.Done()
		arrived.Wait()
	}
	if err := q.Enqueue(Job{Repo: repoNamed("a"), BuildID: "a1", Run: tr.run("a", "a1", hold)}); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(Job{Repo: repoNamed("b"), BuildID: "b1", Run: tr.run("b", "b1", hold)}); err != nil {
		t.Fatal(err)
	}

	// When
	startQueue(t, q)
	select {
	case <-waitBoth(tr.started, 2):
	case <-time.After(5 * time.Second):
		t.Fatal("two jobs from different repos did not start in parallel")
	}

	// Then
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.maxGlobal < 2 {
		t.Errorf("max global concurrency = %d, want >= 2", tr.maxGlobal)
	}
}

// waitBoth returns a channel closed after n values have been read from src.
func waitBoth(src <-chan string, n int) <-chan struct{} {
	out := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			<-src
		}
		close(out)
	}()
	return out
}

func Test_Queue_when_SameRepo_ThenNeverOverlapAndFIFO(t *testing.T) {
	// Given
	q := New(4, 10) // high global concurrency must not break per-repo serial
	tr := newTracker()
	hold := make(chan struct{})
	for _, id := range []string{"a1", "a2", "a3"} {
		if err := q.Enqueue(Job{Repo: repoNamed("a"), BuildID: id, Run: tr.run("a", id, func() { <-hold })}); err != nil {
			t.Fatal(err)
		}
	}

	// When
	startQueue(t, q)
	<-tr.started // a1 begins
	// a2 must not start while a1 is running
	select {
	case id := <-tr.started:
		t.Fatalf("job %q started while a1 still running", id)
	case <-time.After(100 * time.Millisecond):
	}
	close(hold) // let all remaining jobs finish immediately
	for i := 0; i < 2; i++ {
		select {
		case <-tr.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for remaining jobs, got %d", i)
		}
	}

	// Then
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.maxSeen["a"] != 1 {
		t.Errorf("max concurrency for repo a = %d, want 1", tr.maxSeen["a"])
	}
	want := []string{"a1", "a2", "a3"}
	for i, id := range tr.order {
		if i >= len(want) || want[i] != id {
			t.Errorf("order = %v, want %v", tr.order, want)
			break
		}
	}
}

func Test_Enqueue_when_Full_thenReturnErrQueueFull(t *testing.T) {
	// Given
	q := New(1, 1)
	if err := q.Enqueue(Job{Repo: repoNamed("a"), Run: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}

	// When
	err := q.Enqueue(Job{Repo: repoNamed("b"), Run: func(context.Context) error { return nil }})

	// Then
	if !errors.Is(err, ErrQueueFull) {
		t.Errorf("Enqueue() error = %v, want ErrQueueFull", err)
	}
}

func Test_Enqueue_when_Closed_thenReturnErrQueueClosed(t *testing.T) {
	// Given
	q := New(1, 1)
	cancel := startQueue(t, q)
	cancel() // triggers shutdown

	// When (allow shutdown to settle)
	var err error
	for i := 0; i < 100; i++ {
		err = q.Enqueue(Job{Repo: repoNamed("a"), Run: func(context.Context) error { return nil }})
		if errors.Is(err, ErrQueueClosed) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Then
	if !errors.Is(err, ErrQueueClosed) {
		t.Errorf("Enqueue() error = %v, want ErrQueueClosed", err)
	}
}

func Test_Queue_when_Cancelled_ThenStartReturns(t *testing.T) {
	// Given
	q := New(1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// When
	go func() {
		q.Start(ctx)
		close(done)
	}()
	cancel()

	// Then
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after ctx cancel")
	}
}
