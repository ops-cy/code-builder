package webhook

import (
	"strings"
	"testing"
)

// makePushEvent 为 deriveRepository 的测试构造 push 事件：事件负载属于
// 不可信输入，因此测试同时覆盖被接受和被拒绝的形态。
func makePushEvent(name, sshURL, httpURL string) PushEvent {
	var ev PushEvent
	ev.ObjectKind = "push"
	ev.EventName = "push"
	ev.Ref = "refs/heads/main"
	ev.Before = strings.Repeat("1", 40)
	ev.After = strings.Repeat("a", 40)
	ev.CheckoutSHA = strings.Repeat("a", 40)
	ev.Project.Name = name
	ev.Project.GitSSHURL = sshURL
	ev.Project.GitHTTPURL = httpURL
	return ev
}

func Test_normalizeDockerName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "Backend-API", want: "backend-api"},
		{in: "my app", want: "my-app"},
		{in: "v1.2", want: "v1.2"},
		{in: "a--b", want: "a-b"},
		{in: "_edge_", want: "edge"},
		{in: "项目", want: ""},
		{in: "..", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := normalizeDockerName(tt.in); got != tt.want {
				t.Errorf("normalizeDockerName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func Test_hostMatches(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{url: "ssh://git@code.gitlab.com:2222/g/app.git", want: true},
		{url: "https://code.gitlab.com/g/app.git", want: true},
		{url: "https://CODE.GitLab.com/g/app.git", want: true},
		{url: "ssh://git@evilcode.gitlab.com/g/app.git", want: false},
		{url: "ssh://git@code.gitlab.com.evil.io/g/app.git", want: false},
		{url: "not a url", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := hostMatches(tt.url, "code.gitlab.com"); got != tt.want {
				t.Errorf("hostMatches(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func Test_deriveRepository(t *testing.T) {
	t.Run("authorized domain derives repo with name image", func(t *testing.T) {
		ev := makePushEvent("Backend-API",
			"ssh://git@example.com:20022/group/backend-api.git",
			"https://example.com/group/backend-api.git")
		repo, reason := deriveRepository("example.com", newImageRegistry(), ev)
		if reason != "" {
			t.Fatalf("reason = %q, want empty", reason)
		}
		if repo.Name != "backend-api" {
			t.Errorf("Name = %q, want backend-api (sanitised)", repo.Name)
		}
		if repo.Build.Image != "backend-api" {
			t.Errorf("Image = %q, want backend-api (from repository name)", repo.Build.Image)
		}
		if repo.Key == "" || repo.URL == "" {
			t.Errorf("repo = %+v, want key and url filled", repo)
		}
	})

	t.Run("same image name on another repo gets stable suffix", func(t *testing.T) {
		shared := newImageRegistry()
		ev1 := makePushEvent("app",
			"ssh://git@example.com:20022/group/app.git",
			"https://example.com/group/app.git")
		ev2 := makePushEvent("app",
			"ssh://git@example.com:20022/other/app.git",
			"https://example.com/other/app.git")

		r1, _ := deriveRepository("example.com", shared, ev1)
		r2, _ := deriveRepository("example.com", shared, ev2)
		if r1.Build.Image != "app" {
			t.Errorf("first image = %q, want app", r1.Build.Image)
		}
		if r2.Build.Image == "app" {
			t.Errorf("second image = %q, want suffixed name to avoid collision", r2.Build.Image)
		}
		if !strings.HasPrefix(r2.Build.Image, "app-") {
			t.Errorf("second image = %q, want app- prefix", r2.Build.Image)
		}
		// 幂等：同一事件再次投递必须返回同一个镜像名
		r2again, _ := deriveRepository("example.com", shared, ev2)
		if r2again.Build.Image != r2.Build.Image {
			t.Errorf("image not stable: %q vs %q", r2.Build.Image, r2again.Build.Image)
		}
	})

	t.Run("URL last segment wins over the event project name", func(t *testing.T) {
		ev := makePushEvent("项目",
			"ssh://git@example.com:20022/group/repo-name.git",
			"https://example.com/group/repo-name.git")
		repo, reason := deriveRepository("example.com", newImageRegistry(), ev)
		if reason != "" {
			t.Fatalf("reason = %q, want empty", reason)
		}
		if repo.Name != "repo-name" {
			t.Errorf("Name = %q, want repo-name (URL last segment)", repo.Name)
		}
		if repo.Build.Image != repo.Name {
			t.Errorf("Image = %q, want equal to Name %q", repo.Build.Image, repo.Name)
		}
	})

	t.Run("unusable URL name reported as invalid", func(t *testing.T) {
		ev := makePushEvent("app",
			"ssh://git@example.com:20022/group/项目.git",
			"https://example.com/group/项目.git")
		_, reason := deriveRepository("example.com", newImageRegistry(), ev)
		if reason != reasonInvalidName {
			t.Errorf("reason = %q, want %q", reason, reasonInvalidName)
		}
	})

	t.Run("unauthorized domain rejected", func(t *testing.T) {
		ev := makePushEvent("app",
			"ssh://git@evil.example.org/g/app.git",
			"https://evil.example.org/g/app.git")
		_, reason := deriveRepository("example.com", newImageRegistry(), ev)
		if reason != "host_not_allowed" {
			t.Errorf("reason = %q, want host_not_allowed", reason)
		}
	})

	t.Run("missing clone url reported", func(t *testing.T) {
		ev := makePushEvent("app", "", "")
		_, reason := deriveRepository("example.com", newImageRegistry(), ev)
		if reason != "missing_repo_url" {
			t.Errorf("reason = %q, want missing_repo_url", reason)
		}
	})
}

func Test_validCommitSHA(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{in: "a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4", want: true},
		{in: strings.Repeat("a", 64), want: true},
		{in: "", want: false},
		{in: "abc", want: false},
		{in: strings.Repeat("a", 39), want: false},
		{in: strings.Repeat("a", 41), want: false},
		{in: strings.Repeat("z", 40), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := validCommitSHA(tt.in); got != tt.want {
				t.Errorf("validCommitSHA(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func Test_dedup_getPutRelease(t *testing.T) {
	// 准备
	d := newDedup()

	// 执行/断言：put + get 命中
	_, dup := d.get("k@sha")
	if dup {
		t.Fatal("first get must not be duplicate")
	}

	d.put("k@sha", "build-1")
	_, dup = d.get("k@sha")
	if !dup {
		t.Error("second get must be duplicate while build is active")
	}

	// 用不匹配的 id 调用 release 不得清除该条目
	d.release("k@sha", "other-build")
	if _, dup := d.get("k@sha"); !dup {
		t.Error("release with wrong id must not clear the dedup entry")
	}

	// 用匹配的 id 调用 release 会清除该条目
	d.release("k@sha", "build-1")
	if _, dup := d.get("k@sha"); dup {
		t.Error("release with matching id must clear the dedup entry")
	}
}

func Test_imageRegistry_stableAcrossCalls(t *testing.T) {
	// 准备
	reg := newImageRegistry()

	// 执行
	first := reg.resolve("app", "example.com/g/app")
	second := reg.resolve("app", "example.com/g/app")
	conflict := reg.resolve("app", "example.com/other/app")

	// 断言
	if first != "app" {
		t.Errorf("first = %q, want app", first)
	}
	if second != first {
		t.Errorf("second = %q, want stable %q", second, first)
	}
	if conflict == "app" {
		t.Errorf("conflict = %q, want suffixed image", conflict)
	}
	if !strings.HasPrefix(conflict, "app-") {
		t.Errorf("conflict = %q, want app- prefix", conflict)
	}
}
