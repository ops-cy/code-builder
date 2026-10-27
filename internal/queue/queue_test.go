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

// startQueue 运行 q 直到 ctx 取消；返回一个在退出时关闭的 done 通道。
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

// tracker 记录每个仓库的并发情况和启动顺序。
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
	// 准备
	q := New(2, 10)
	tr := newTracker()
	var arrived sync.WaitGroup
	arrived.Add(2)
	// 两个任务都先调用 Done 再调用 Wait：在两者都进入运行状态前都不会返回。
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

	// 执行
	startQueue(t, q)
	select {
	case <-waitBoth(tr.started, 2):
	case <-time.After(5 * time.Second):
		t.Fatal("two jobs from different repos did not start in parallel")
	}

	// 断言
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.maxGlobal < 2 {
		t.Errorf("max global concurrency = %d, want >= 2", tr.maxGlobal)
	}
}

// waitBoth 返回一个通道，在从 src 读取 n 个值后关闭。
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
	// 准备
	q := New(4, 10) // 高全局并发不得破坏单仓库串行
	tr := newTracker()
	hold := make(chan struct{})
	for _, id := range []string{"a1", "a2", "a3"} {
		if err := q.Enqueue(Job{Repo: repoNamed("a"), BuildID: id, Run: tr.run("a", id, func() { <-hold })}); err != nil {
			t.Fatal(err)
		}
	}

	// 执行
	startQueue(t, q)
	<-tr.started // a1 开始
	// a1 运行期间 a2 不得启动
	select {
	case id := <-tr.started:
		t.Fatalf("job %q started while a1 still running", id)
	case <-time.After(100 * time.Millisecond):
	}
	close(hold) // 让所有剩余任务立即结束
	for i := 0; i < 2; i++ {
		select {
		case <-tr.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for remaining jobs, got %d", i)
		}
	}

	// 断言
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

func Test_repoKey_when_NameSet_thenNameWins(t *testing.T) {
	// 准备：通过两个不同 clone URL 访问到同一个 repo_name。
	j := Job{Repo: config.Repository{Key: "example.com/a/app", Name: "app"}}

	// 断言
	if got := repoKey(j); got != "app" {
		t.Errorf("repoKey() = %q, want app (repo_name)", got)
	}
}

func Test_repoKey_when_NameEmpty_thenFallBackToKey(t *testing.T) {
	j := Job{Repo: config.Repository{Key: "example.com/a/app"}}
	if got := repoKey(j); got != "example.com/a/app" {
		t.Errorf("repoKey() = %q, want the clone URL", got)
	}
}

func Test_Queue_when_SameRepoNameDifferentURLs_thenSerialised(t *testing.T) {
	// 准备：两个 clone URL 解析到同一个 repo_name。
	q := New(4, 10)
	tr := newTracker()
	hold := make(chan struct{})
	if err := q.Enqueue(Job{
		Repo:    config.Repository{Key: "example.com/team/app", Name: "app"},
		BuildID: "u1",
		Run:     tr.run("app", "u1", func() { <-hold }),
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(Job{
		Repo:    config.Repository{Key: "example.com/other/app", Name: "app"},
		BuildID: "u2",
		Run:     tr.run("app", "u2", nil),
	}); err != nil {
		t.Fatal(err)
	}

	// 执行
	startQueue(t, q)
	<-tr.started // u1 开始

	// 断言：即使 URL 不同，u2 也不得与 u1 重叠。
	select {
	case id := <-tr.started:
		t.Fatalf("job %q started while another app build was running", id)
	case <-time.After(100 * time.Millisecond):
	}
	close(hold)
	select {
	case <-tr.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the second job")
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.maxSeen["app"] != 1 {
		t.Errorf("max concurrency for app = %d, want 1", tr.maxSeen["app"])
	}
}

func Test_Enqueue_when_Full_thenReturnErrQueueFull(t *testing.T) {
	// 准备
	q := New(1, 1)
	if err := q.Enqueue(Job{Repo: repoNamed("a"), Run: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}

	// 执行
	err := q.Enqueue(Job{Repo: repoNamed("b"), Run: func(context.Context) error { return nil }})

	// 断言
	if !errors.Is(err, ErrQueueFull) {
		t.Errorf("Enqueue() error = %v, want ErrQueueFull", err)
	}
}

func Test_Enqueue_when_Closed_thenReturnErrQueueClosed(t *testing.T) {
	// 准备
	q := New(1, 1)
	cancel := startQueue(t, q)
	cancel() // 触发关闭

	// 执行（等待关闭完成）
	var err error
	for i := 0; i < 100; i++ {
		err = q.Enqueue(Job{Repo: repoNamed("a"), Run: func(context.Context) error { return nil }})
		if errors.Is(err, ErrQueueClosed) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 断言
	if !errors.Is(err, ErrQueueClosed) {
		t.Errorf("Enqueue() error = %v, want ErrQueueClosed", err)
	}
}

func Test_Queue_when_Cancelled_ThenStartReturns(t *testing.T) {
	// 准备
	q := New(1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// 执行
	go func() {
		q.Start(ctx)
		close(done)
	}()
	cancel()

	// 断言
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after ctx cancel")
	}
}
