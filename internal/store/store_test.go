package store

import (
	"errors"
	"io/fs"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir())
}

func Test_Create_when_NewBuild_thenReturnAssignsUniqueIDsAndLogPath(t *testing.T) {
	// Given
	s := newTestStore(t)

	// When
	a := s.Create(Build{Repo: "app", Branch: "main", SHA: "abc", Status: StatusQueued, CreatedAt: time.Now()})
	b := s.Create(Build{Repo: "app", Branch: "main", SHA: "def", Status: StatusQueued, CreatedAt: time.Now()})

	// Then
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

func Test_Get_when_UnknownID_thenReturnFalse(t *testing.T) {
	// Given
	s := newTestStore(t)

	// When
	_, ok := s.Get("bogus")

	// Then
	if ok {
		t.Error("Get(bogus) ok = true, want false")
	}
}

func Test_Get_when_MutatedCopy_thenReturnOriginalUnchanged(t *testing.T) {
	// Given
	s := newTestStore(t)
	created := s.Create(Build{Repo: "app", Status: StatusQueued, CreatedAt: time.Now()})

	// When
	got, _ := s.Get(created.ID)
	got.Status = StatusFailed

	// Then
	again, _ := s.Get(created.ID)
	if again.Status != StatusQueued {
		t.Errorf("Status = %q after mutating copy, want queued", again.Status)
	}
}

func Test_List_when_MultipleBuilds_thenReturnNewestFirst(t *testing.T) {
	// Given
	s := newTestStore(t)
	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	s.Create(Build{Repo: "old", CreatedAt: older})
	s.Create(Build{Repo: "new", CreatedAt: newer})

	// When
	list := s.List()

	// Then
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
	if list[0].Repo != "new" {
		t.Errorf("List()[0].Repo = %q, want new (newest first)", list[0].Repo)
	}
}

func Test_Update_when_UnknownID_thenReturnNoOp(t *testing.T) {
	// Given
	s := newTestStore(t)

	// When
	s.Update("bogus", func(b *Build) { b.Status = StatusFailed })

	// Then — no panic, no build created
	if len(s.List()) != 0 {
		t.Errorf("List() len = %d, want 0", len(s.List()))
	}
}

func Test_LogRoundtrip_when_WriteThenRead_thenReturnContent(t *testing.T) {
	// Given
	s := newTestStore(t)
	b := s.Create(Build{Repo: "app", CreatedAt: time.Now()})

	// When
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

	// Then
	if err != nil {
		t.Fatalf("ReadLog() error = %v", err)
	}
	if got != "hello build log\n" {
		t.Errorf("ReadLog() = %q, want %q", got, "hello build log\n")
	}
}

func Test_ReadLog_when_UnknownID_thenReturnError(t *testing.T) {
	// Given
	s := newTestStore(t)

	// When
	_, err := s.ReadLog("bogus")

	// Then
	if err == nil {
		t.Fatal("ReadLog(bogus) error = nil, want error")
	}
}

func Test_ReadLog_when_FileMissing_thenReturnNotExist(t *testing.T) {
	// Given
	s := newTestStore(t)
	b := s.Create(Build{Repo: "app", CreatedAt: time.Now()})

	// When
	_, err := s.ReadLog(b.ID)

	// Then
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadLog() error = %v, want fs.ErrNotExist", err)
	}
}

func Test_Create_when_Concurrent_thenReturnUniqueIDs(t *testing.T) {
	// Given
	s := newTestStore(t)
	const n = 10
	var wg sync.WaitGroup
	ids := make([]string, n)

	// When
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			ids[i] = s.Create(Build{Repo: "app", CreatedAt: time.Now()}).ID
		}(i)
	}
	wg.Wait()

	// Then
	seen := make(map[string]bool, n)
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate ID %q", id)
		}
		seen[id] = true
	}
}
