// Package store 在内存中保存构建记录，在磁盘上保存构建日志，并可将构建
// 记录持久化为 JSON，避免服务重启后丢失历史。
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status 是构建的生命周期状态。
type Status string

// 构建状态。
const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
)

// Build 是一条构建记录。
type Build struct {
	ID            string
	Repo          string
	FullName      string // 平台上的完整仓库名，例如 owner/repo
	Namespace     string
	Provider      string // gitlab / github / gitee
	Branch        string
	SHA           string
	ShortSHA      string
	Actor         string
	CommitSubject string
	Status        Status
	Tag           string
	ImageTags     []string
	Deployed      bool
	DeployStatus  string
	DeployReason  string
	DeployTarget  string
	DeployError   string
	DeployOutput  string
	NotifyError   string
	Error         string
	LogPath       string
	CreatedAt     time.Time
	StartedAt     time.Time
	FinishedAt    time.Time
}

// Store 是并发安全的构建内存注册表；日志正文存放在 logsDir 下，构建记录
// 可选地持久化到 statePath。
type Store struct {
	mu        sync.RWMutex
	logs      string
	statePath string
	nextID    int
	builds    map[string]*Build
}

// New 在日志目录不存在时创建它，并返回空 store（不持久化构建记录）。
func New(logsDir string) *Store {
	return NewPersistent(logsDir, "")
}

// NewPersistent 创建 store，并在 statePath 非空时加载已有构建记录。
func NewPersistent(logsDir, statePath string) *Store {
	_ = os.MkdirAll(logsDir, 0o755)
	s := &Store{
		logs:      logsDir,
		statePath: statePath,
		builds:    make(map[string]*Build),
	}
	if statePath != "" {
		s.load()
	}
	return s
}

type persistedState struct {
	NextID int     `json:"next_id"`
	Builds []Build `json:"builds"`
}

func (s *Store) load() {
	data, err := os.ReadFile(s.statePath)
	if err != nil {
		return
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		fmt.Fprintf(os.Stderr, "store: load %s: %v\n", s.statePath, err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID = st.NextID
	for i := range st.Builds {
		b := st.Builds[i]
		if b.ID == "" {
			continue
		}
		if n := idNumber(b.ID); n > s.nextID {
			s.nextID = n
		}
		s.builds[b.ID] = &b
	}
}

// Create 注册构建，分配 ID 和 LogPath，并返回存储的副本。
func (s *Store) Create(b Build) Build {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := fmt.Sprintf("b-%d-%s", s.nextID, randomHex(3))
	b.ID = id
	b.LogPath = filepath.Join(s.logs, id+".log")
	stored := b
	s.builds[id] = &stored
	s.saveLocked()
	return stored
}

// Get 返回指定 ID 的构建副本。
func (s *Store) Get(id string) (Build, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.builds[id]
	if !ok {
		return Build{}, false
	}
	return *b, true
}

// List 返回所有构建的副本，按 CreatedAt 从新到旧排序。
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

// Update 就地修改指定 ID 的构建；未知 ID 不做任何操作。
func (s *Store) Update(id string, fn func(*Build)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.builds[id]; ok {
		fn(b)
		s.saveLocked()
	}
}

// OpenLog 以追加方式打开构建日志文件。由调用方负责关闭。
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

// ReadLog 返回构建的完整日志内容。
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

// ReadLogTail 最多返回构建日志的最后 n 行。
func (s *Store) ReadLogTail(id string, n int) string {
	if n <= 0 {
		return ""
	}
	content, err := s.ReadLog(id)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(content, "\r\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// saveLocked 原子写入构建记录；调用方必须持有写锁。
func (s *Store) saveLocked() {
	if s.statePath == "" {
		return
	}
	st := persistedState{NextID: s.nextID, Builds: make([]Build, 0, len(s.builds))}
	for _, b := range s.builds {
		st.Builds = append(st.Builds, *b)
	}
	sort.Slice(st.Builds, func(i, j int) bool { return st.Builds[i].CreatedAt.Before(st.Builds[j].CreatedAt) })
	data, err := json.Marshal(st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: marshal state: %v\n", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "store: create state dir: %v\n", err)
		return
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "store: write state: %v\n", err)
		return
	}
	if err := os.Rename(tmp, s.statePath); err != nil {
		fmt.Fprintf(os.Stderr, "store: replace state: %v\n", err)
	}
}

func idNumber(id string) int {
	parts := strings.Split(id, "-")
	if len(parts) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(parts[1])
	return n
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "000000"
	}
	return hex.EncodeToString(buf)
}

// ErrNotExist 是便于调用方检查日志缺失而重新导出的错误。
var ErrNotExist = fs.ErrNotExist
