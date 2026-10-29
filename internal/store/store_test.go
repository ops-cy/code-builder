package store

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir())
}

func Test_Create_when_NewBuild_thenReturnAssignsUniqueIDsAndLogPath(t *testing.T) {
	// 准备
	s := newTestStore(t)

	// 执行
	a := s.Create(Build{Repo: "app", Branch: "main", SHA: "abc", Status: StatusQueued, CreatedAt: time.Now()})
	b := s.Create(Build{Repo: "app", Branch: "main", SHA: "def", Status: StatusQueued, CreatedAt: time.Now()})

	// 断言
	if a.ID == "" || b.ID == "" {
		t.Fatal("Create() returned empty ID")
	}
	if a.ID == b.ID {
		t.Errorf("duplicate IDs: %q", a.ID)
	}
	if a.LogPath == "" {
		t.Error("LogPath empty")
	}
}

func Test_NewPersistent_when_Reopened_thenBuildsAndIDsRestored(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "builds.json")

	s := NewPersistent(dir, state)
	b := s.Create(Build{
		Repo:      "app",
		FullName:  "acme/app",
		Provider:  "github",
		Branch:    "main",
		Status:    StatusSuccess,
		CreatedAt: time.Now(),
	})
	s.Update(b.ID, func(bb *Build) { bb.Tag = "latest" })

	reopened := NewPersistent(dir, state)
	got, ok := reopened.Get(b.ID)
	if !ok {
		t.Fatalf("build %q not restored", b.ID)
	}
	if got.Provider != "github" || got.FullName != "acme/app" || got.Tag != "latest" {
		t.Errorf("restored build = %+v", got)
	}

	next := reopened.Create(Build{Repo: "next", CreatedAt: time.Now()})
	if next.ID == b.ID {
		t.Errorf("ID %q reused after restore", next.ID)
	}
	if strings.Compare(next.ID, b.ID) == 0 {
		t.Errorf("next ID = %q, want a new ID", next.ID)
	}
}

func Test_Get_when_UnknownID_thenReturnFalse(t *testing.T) {
	// 准备
	s := newTestStore(t)

	// 执行
	_, ok := s.Get("bogus")

	// 断言
	if ok {
		t.Error("Get(bogus) ok = true, want false")
	}
}

func Test_Get_when_MutatedCopy_thenReturnOriginalUnchanged(t *testing.T) {
	// 准备
	s := newTestStore(t)
	created := s.Create(Build{Repo: "app", Status: StatusQueued, CreatedAt: time.Now()})

	// 执行
	got, _ := s.Get(created.ID)
	got.Status = StatusFailed

	// 断言
	again, _ := s.Get(created.ID)
	if again.Status != StatusQueued {
		t.Errorf("Status = %q after mutating copy, want queued", again.Status)
	}
}

func Test_List_when_MultipleBuilds_thenReturnNewestFirst(t *testing.T) {
	// 准备
	s := newTestStore(t)
	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	s.Create(Build{Repo: "old", CreatedAt: older})
	s.Create(Build{Repo: "new", CreatedAt: newer})

	// 执行
	list := s.List()

	// 断言
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	if list[0].Repo != "new" {
		t.Errorf("List()[0].Repo = %q, want new (newest first)", list[0].Repo)
	}
}

func Test_Update_when_UnknownID_thenReturnNoOp(t *testing.T) {
	// 准备
	s := newTestStore(t)

	// 执行
	s.Update("bogus", func(b *Build) { b.Status = StatusFailed })

	// 断言 — 不 panic，也不创建构建
	if len(s.List()) != 0 {
		t.Errorf("List() len = %d, want 0", len(s.List()))
	}
}

func Test_LogRoundtrip_when_WriteThenRead_thenReturnContent(t *testing.T) {
	// 准备
	s := newTestStore(t)
	b := s.Create(Build{Repo: "app", CreatedAt: time.Now()})

	// 执行
	f, err := s.OpenLog(b.ID)
	if err != nil {
		t.Fatalf("OpenLog() error = %v", err)
	}
	if _, err := f.WriteString("hello build log\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, err := s.ReadLog(b.ID)

	// 断言
	if err != nil {
		t.Fatalf("ReadLog() error = %v", err)
	}
	if got != "hello build log\n" {
		t.Errorf("ReadLog() = %q, want %q", got, "hello build log\n")
	}
}

func Test_ReadLog_when_UnknownID_thenReturnError(t *testing.T) {
	// 准备
	s := newTestStore(t)

	// 执行
	_, err := s.ReadLog("bogus")

	// 断言
	if err == nil {
		t.Fatal("ReadLog(bogus) error = nil, want error")
	}
}

func Test_ReadLog_when_FileMissing_thenReturnNotExist(t *testing.T) {
	// 准备
	s := newTestStore(t)
	b := s.Create(Build{Repo: "app", CreatedAt: time.Now()})

	// 执行
	_, err := s.ReadLog(b.ID)

	// 断言
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadLog() error = %v, want fs.ErrNotExist", err)
	}
}

func Test_ReadLogTail_when_LongLog_thenReturnsLastLines(t *testing.T) {
	// 准备
	s := newTestStore(t)
	b := s.Create(Build{Repo: "app", CreatedAt: time.Now()})
	f, err := s.OpenLog(b.ID)
	if err != nil {
		t.Fatalf("OpenLog() error = %v", err)
	}
	var want strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(f, "line %02d\n", i)
		if i >= 10 {
			fmt.Fprintf(&want, "line %02d\n", i)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 执行
	got := s.ReadLogTail(b.ID, 30)

	// 断言
	if got != strings.TrimRight(want.String(), "\n") {
		t.Errorf("ReadLogTail() = %q, want the last 30 lines", got)
	}
}

func Test_ReadLogTail_when_ShortLog_thenReturnsEverything(t *testing.T) {
	// 准备
	s := newTestStore(t)
	b := s.Create(Build{Repo: "app", CreatedAt: time.Now()})
	f, _ := s.OpenLog(b.ID)
	_, _ = f.WriteString("alpha\nbeta\n")
	_ = f.Close()

	// 执行
	got := s.ReadLogTail(b.ID, 30)

	// 断言
	if got != "alpha\nbeta" {
		t.Errorf("ReadLogTail() = %q, want alpha\\nbeta", got)
	}
}

func Test_ReadLogTail_when_InvalidInput_thenEmpty(t *testing.T) {
	// 准备
	s := newTestStore(t)
	b := s.Create(Build{Repo: "app", CreatedAt: time.Now()})

	// 执行 / 断言
	if got := s.ReadLogTail(b.ID, 0); got != "" {
		t.Errorf("ReadLogTail(0) = %q, want empty", got)
	}
	if got := s.ReadLogTail("bogus", 10); got != "" {
		t.Errorf("ReadLogTail(unknown) = %q, want empty", got)
	}
}

func Test_Create_when_Concurrent_thenReturnUniqueIDs(t *testing.T) { // 准备
	s := newTestStore(t)
	const n = 10
	var wg sync.WaitGroup
	ids := make([]string, n)

	// 执行
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			ids[i] = s.Create(Build{Repo: "app", CreatedAt: time.Now()}).ID
		}(i)
	}
	wg.Wait()

	// 断言
	seen := make(map[string]bool, n)
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate ID %q", id)
		}
		seen[id] = true
	}
}
