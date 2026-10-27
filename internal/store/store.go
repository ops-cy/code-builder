// Package store 在内存中保存构建记录，在磁盘上保存构建日志。
package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	Branch        string
	SHA           string
	ShortSHA      string
	Actor         string // webhook 事件中的推送发起人
	CommitSubject string // webhook 事件中的单行提交摘要
	Status        Status
	Tag           string // 本次构建解析出的镜像 tag
	ImageTags     []string
	Deployed      bool   // 构建后部署成功时为 true
	DeployStatus  string // 取值："", "skipped", "deployed", "failed"
	DeployReason  string // 跳过部署的原因
	DeployTarget  string // "<目录>/<compose 文件>"
	DeployError   string // 部署失败时非空
	DeployOutput  string // 部署失败后的 `docker compose ps` 输出
	NotifyError   string // 通知投递失败时非空
	Error         string
	LogPath       string
	CreatedAt     time.Time
	StartedAt     time.Time
	FinishedAt    time.Time
}

// Store 是并发安全的构建内存注册表；日志正文存放在 logsDir 下的磁盘上。
type Store struct {
	mu     sync.RWMutex
	logs   string
	nextID int
	builds map[string]*Build
}

// New 在日志目录不存在时创建它，并返回空 store。
func New(logsDir string) *Store {
	_ = os.MkdirAll(logsDir, 0o755)
	return &Store{
		logs:   logsDir,
		builds: make(map[string]*Build),
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

// ReadLogTail 最多返回构建日志的最后 n 行，用于在通知中嵌入简短的
// 失败片段。日志不存在时返回 ""。
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

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "000000"
	}
	return hex.EncodeToString(buf)
}

// ErrNotExist 是便于调用方检查日志缺失而重新导出的错误。
var ErrNotExist = fs.ErrNotExist
