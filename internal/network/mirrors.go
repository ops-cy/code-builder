// Package network 负责根据构建机所在网络环境选择软件包下载源。
package network

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"code-builder/internal/config"
)

// Region 是探测得到的网络区域。
type Region string

const (
	RegionCN     Region = "cn"
	RegionGlobal Region = "global"
)

// Mirrors 是本次构建要使用的镜像源。
type Mirrors struct {
	Region        string
	GoProxy       string
	GoSumDB       string
	NPMRegistry   string
	MavenMirror   string
	GradleMirror  string
	CargoRegistry string
	APKMirror     string
	APTMirror     string
}

// Resolver 探测并缓存网络区域与镜像源。
type Resolver struct {
	cfg    config.NetworkConfig
	logger *slog.Logger

	mu     sync.Mutex
	cached Mirrors
	at     time.Time

	// probe 是测试接缝；返回每个探测目标的延迟。
	probe func(ctx context.Context, urls []string) (time.Duration, int)
}

// New 创建 Resolver。
func New(cfg config.NetworkConfig, logger *slog.Logger) *Resolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &Resolver{cfg: cfg, logger: logger, probe: probe}
}

// Get 返回当前应使用的镜像源。auto 模式会探测并缓存。
func (r *Resolver) Get(ctx context.Context) Mirrors {
	if r == nil {
		return forRegion(RegionGlobal, config.NetworkConfig{})
	}
	region := strings.ToLower(strings.TrimSpace(r.cfg.Region))
	if region == "cn" || region == "global" {
		return forRegion(Region(region), r.cfg)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	ttl := parseDuration(r.cfg.CacheTTL, 10*time.Minute)
	if !r.at.IsZero() && time.Since(r.at) < ttl {
		return r.cached
	}

	probeTimeout := parseDuration(r.cfg.ProbeTimeout, 3*time.Second)
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	cnLatency, cnOK := r.probe(pctx, cnProbes)
	globalLatency, globalOK := r.probe(pctx, globalProbes)

	chosen := RegionGlobal
	switch {
	case cnOK > 0 && globalOK == 0:
		chosen = RegionCN
	case cnOK == 0 && globalOK > 0:
		chosen = RegionGlobal
	case cnOK > 0 && globalOK > 0 && cnLatency < globalLatency:
		chosen = RegionCN
	}

	r.cached = forRegion(chosen, r.cfg)
	r.at = time.Now()
	r.logger.Info("network: mirrors resolved",
		slog.String("region", string(chosen)),
		slog.String("goproxy", r.cached.GoProxy),
		slog.String("npm", r.cached.NPMRegistry),
		slog.String("maven", r.cached.MavenMirror))
	return r.cached
}

var (
	cnProbes = []string{
		"https://goproxy.cn",
		"https://registry.npmmirror.com",
		"https://maven.aliyun.com/repository/public",
		"https://rsproxy.cn",
	}
	globalProbes = []string{
		"https://proxy.golang.org",
		"https://registry.npmjs.org",
		"https://repo.maven.apache.org/maven2",
		"https://crates.io",
	}
)

// probe 并发请求探测目标，返回平均延迟与成功数量。
func probe(ctx context.Context, urls []string) (time.Duration, int) {
	client := &http.Client{Timeout: 2 * time.Second}
	type result struct {
		ok      bool
		latency time.Duration
	}
	ch := make(chan result, len(urls))
	for _, u := range urls {
		go func(url string) {
			start := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				ch <- result{}
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				ch <- result{}
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 500 {
				ch <- result{}
				return
			}
			ch <- result{ok: true, latency: time.Since(start)}
		}(u)
	}
	var total time.Duration
	var ok int
	for range urls {
		r := <-ch
		if r.ok {
			ok++
			total += r.latency
		}
	}
	if ok == 0 {
		return 0, 0
	}
	return total / time.Duration(ok), ok
}

func forRegion(region Region, cfg config.NetworkConfig) Mirrors {
	m := Mirrors{Region: string(region)}
	if region == RegionCN {
		m.GoProxy = "https://goproxy.cn,direct"
		m.GoSumDB = "sum.golang.org"
		m.NPMRegistry = "https://registry.npmmirror.com"
		m.MavenMirror = "https://maven.aliyun.com/repository/public"
		m.GradleMirror = "https://maven.aliyun.com/repository/public"
		m.CargoRegistry = "sparse+https://rsproxy.cn/index/"
		m.APKMirror = "mirrors.aliyun.com"
		m.APTMirror = "mirrors.aliyun.com"
	} else {
		m.GoProxy = "https://proxy.golang.org,direct"
		m.GoSumDB = "sum.golang.org"
	}
	override(&m.GoProxy, cfg.GoProxy)
	override(&m.GoSumDB, cfg.GoSumDB)
	override(&m.NPMRegistry, cfg.NPMRegistry)
	override(&m.MavenMirror, cfg.MavenMirror)
	override(&m.GradleMirror, cfg.GradleMirror)
	override(&m.CargoRegistry, cfg.CargoRegistry)
	override(&m.APKMirror, cfg.APKMirror)
	override(&m.APTMirror, cfg.APTMirror)
	return m
}

func override(dst *string, value string) {
	if strings.TrimSpace(value) != "" {
		*dst = strings.TrimSpace(value)
	}
}

func parseDuration(value string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(value); err == nil && d > 0 {
		return d
	}
	return def
}

// String 便于日志与测试。
func (m Mirrors) String() string {
	return fmt.Sprintf("region=%s goproxy=%s npm=%s maven=%s", m.Region, m.GoProxy, m.NPMRegistry, m.MavenMirror)
}
