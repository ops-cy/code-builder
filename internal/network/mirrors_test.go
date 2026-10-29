package network

import (
	"context"
	"testing"
	"time"

	"code-builder/internal/config"
)

func Test_forRegion_cnAndGlobal(t *testing.T) {
	cn := forRegion(RegionCN, config.NetworkConfig{})
	if cn.GoProxy == "" || cn.NPMRegistry != "https://registry.npmmirror.com" || cn.APKMirror == "" {
		t.Errorf("cn mirrors = %+v", cn)
	}
	global := forRegion(RegionGlobal, config.NetworkConfig{})
	if global.NPMRegistry != "" || global.MavenMirror != "" {
		t.Errorf("global default mirrors should be empty: %+v", global)
	}
	if global.GoProxy == "" {
		t.Error("global GoProxy should default to proxy.golang.org")
	}
}

func Test_forRegion_overrides(t *testing.T) {
	m := forRegion(RegionCN, config.NetworkConfig{
		NPMRegistry:   "https://npm.example.com",
		MavenMirror:   "https://maven.example.com",
		APTMirror:     "mirrors.example.com",
		CargoRegistry: "sparse+https://cargo.example.com/index/",
	})
	if m.NPMRegistry != "https://npm.example.com" || m.MavenMirror != "https://maven.example.com" {
		t.Errorf("overrides not applied: %+v", m)
	}
	if m.APTMirror != "mirrors.example.com" || m.CargoRegistry != "sparse+https://cargo.example.com/index/" {
		t.Errorf("overrides not applied: %+v", m)
	}
}

func Test_Resolver_explicitRegion(t *testing.T) {
	r := New(config.NetworkConfig{Region: "cn"}, nil)
	m := r.Get(context.Background())
	if m.Region != "cn" || m.NPMRegistry == "" {
		t.Errorf("mirrors = %+v", m)
	}
}

func Test_Resolver_autoChoosesFasterRegion(t *testing.T) {
	r := New(config.NetworkConfig{Region: "auto", ProbeTimeout: "1s", CacheTTL: "1m"}, nil)
	r.probe = func(_ context.Context, urls []string) (time.Duration, int) {
		if urls[0] == cnProbes[0] {
			return 20 * time.Millisecond, len(urls)
		}
		return 200 * time.Millisecond, len(urls)
	}
	m := r.Get(context.Background())
	if m.Region != "cn" {
		t.Errorf("region = %q, want cn", m.Region)
	}
	// 第二次调用应命中缓存，不再探测。
	r.probe = func(context.Context, []string) (time.Duration, int) {
		t.Fatal("probe called despite cache")
		return 0, 0
	}
	_ = r.Get(context.Background())
}

func Test_Resolver_autoFallsBackToGlobal(t *testing.T) {
	r := New(config.NetworkConfig{Region: "auto", ProbeTimeout: "1s", CacheTTL: "1m"}, nil)
	r.probe = func(_ context.Context, urls []string) (time.Duration, int) {
		if urls[0] == cnProbes[0] {
			return 0, 0
		}
		return 30 * time.Millisecond, len(urls)
	}
	if m := r.Get(context.Background()); m.Region != "global" {
		t.Errorf("region = %q, want global", m.Region)
	}
}
