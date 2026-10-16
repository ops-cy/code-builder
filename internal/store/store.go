// Package store keeps build records in memory and build logs on disk.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Status is the lifecycle state of a build.
type Status string

// Build statuses.
const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
)

// Build is one build record.
type Build struct {
	ID         string
	Repo       string
	Branch     string
	SHA        string
	ShortSHA   string
	Status     Status
	ImageTags  []string
	Error      string
	LogPath    string
	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// Store is a concurrency-safe in-memory registry of builds; log bodies live
// on disk under logsDir.
type Store struct {
	mu     sync.RWMutex
	logs   string
	nextID int
	builds map[string]*Build
}

// New creates the log directory if missing and returns an empty store.
func New(logsDir string) *Store {
	_ = os.MkdirAll(logsDir, 0o755)
	return &Store{
		logs:   logsDir,
		builds: make(map[string]*Build),
	}
}

// Create registers the build, assigns ID and LogPath, and returns the stored copy.
func (s *Store) Create(b Build) Build {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := fmt.Sprintf("b-%d-%s", s.nextID, randomHex(3))
	b.ID = id
	b.LogPath = filepath.Join(s.logs, id+".log")
	stored := b
	s.builds[id] = &stored
	return stored
}

// Get returns a copy of the build with the given ID.
func (s *Store) Get(id string) (Build, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.builds[id]
	if !ok {
		return Build{}, false
	}
	return *b, true
}

// List returns copies of all builds, newest CreatedAt first.
func (s *Store) List() []Build {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Build, 0, len(s.builds))
	for _, b := range s.builds {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Update mutates the build with the given ID in place; unknown IDs are a no-op.
func (s *Store) Update(id string, fn func(*Build)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.builds[id]; ok {
		fn(b)
	}
}

// OpenLog opens the build's log file for appending. Caller closes it.
func (s *Store) OpenLog(id string) (*os.File, error) {
	s.mu.RLock()
	b, ok := s.builds[id]
	var path string
	if ok {
		path = b.LogPath
	}
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("store: unknown build %q", id)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("store: open log for %q: %w", id, err)
	}
	return f, nil
}

// ReadLog returns the full log content of a build.
func (s *Store) ReadLog(id string) (string, error) {
	s.mu.RLock()
	b, ok := s.builds[id]
	var path string
	if ok {
		path = b.LogPath
	}
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("store: unknown build %q", id)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("store: read log for %q: %w", id, err)
	}
	return string(content), nil
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "000000"
	}
	return hex.EncodeToString(buf)
}

// ErrNotExist re-exported convenience for callers checking log absence.
var ErrNotExist = fs.ErrNotExist
