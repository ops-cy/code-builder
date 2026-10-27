package deploy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess 不是真正的测试：它是被打桩的 execCommand 启动的
// 子进程，使 Up/Ps 无需 docker 也能运行真实命令。
func TestHelperProcess(t *testing.T) {
	if os.Getenv("DEPLOY_HELPER") != "1" {
		return
	}
	wd, _ := os.Getwd()
	fmt.Printf("cwd=%s\nargs=%s\n", wd, strings.Join(os.Args[1:], " "))
	if os.Getenv("DEPLOY_HELPER_SLEEP") == "1" {
		time.Sleep(10 * time.Second)
	}
	if os.Getenv("DEPLOY_HELPER_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "compose boom")
		os.Exit(1)
	}
	os.Exit(0)
}

// stubExec 用辅助进程替换命令接缝，并在测试结束时恢复。
func stubExec(t *testing.T) {
	t.Helper()
	old := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		all := append([]string{"-test.run=TestHelperProcess", "--", name}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], all...)
		cmd.Env = append(os.Environ(), "DEPLOY_HELPER=1")
		return cmd
	}
	t.Cleanup(func() { execCommand = old })
}

// targetDir 创建一个包含 compose 文件的服务目录。
func targetDir(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "docker-compose.yaml")
	if err := os.WriteFile(file, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	return dir, file
}

func Test_upArgs(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		service string
		want    []string
	}{
		{
			name: "no service starts the whole file",
			file: "/s/docker-compose.yaml",
			want: []string{"compose", "-f", "/s/docker-compose.yaml", "up", "-d"},
		},
		{
			name:    "service is appended last",
			file:    "/s/docker-compose.yaml",
			service: "api",
			want:    []string{"compose", "-f", "/s/docker-compose.yaml", "up", "-d", "api"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := upArgs(tt.file, tt.service)
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("upArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_psArgs(t *testing.T) {
	got := psArgs("/s/docker-compose.yaml")
	want := []string{"compose", "-f", "/s/docker-compose.yaml", "ps"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("psArgs() = %v, want %v", got, want)
	}
}

func Test_Up_when_ServiceEmpty_thenRunsWholeFile(t *testing.T) {
	// 准备
	stubExec(t)
	dir, file := targetDir(t)
	var log bytes.Buffer

	// 执行
	err := Up(context.Background(), Request{
		Target: Target{Dir: dir, ComposeFile: filepath.Base(file)},
		Log:    &log,
	})

	// 断言
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	out := log.String()
	if !strings.Contains(out, "up -d") {
		t.Errorf("log missing up -d: %q", out)
	}
	if !strings.Contains(out, "up -d (dir=") {
		t.Errorf("log missing the directory-tagged up command: %q", out)
	}
	if strings.Contains(out, "up -d api") || strings.Contains(out, "up -d  ") {
		t.Errorf("log appended a service for an empty service: %q", out)
	}
	if !strings.Contains(out, "cwd="+dir) {
		t.Errorf("command did not run with Dir=%s: %q", dir, out)
	}
}

func Test_Up_when_ServiceSet_thenAppendsService(t *testing.T) {
	// 准备
	stubExec(t)
	dir, file := targetDir(t)
	var log bytes.Buffer

	// 执行
	err := Up(context.Background(), Request{
		Target: Target{Dir: dir, ComposeFile: filepath.Base(file), Service: "api"},
		Log:    &log,
	})

	// 断言
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if !strings.Contains(log.String(), "up -d api") {
		t.Errorf("log missing the service argument: %q", log.String())
	}
}

func Test_Up_when_CommandFails_thenErrorCarriesTail(t *testing.T) {
	// 准备
	stubExec(t)
	t.Setenv("DEPLOY_HELPER_FAIL", "1") // 子进程会继承 os.Environ()
	dir, file := targetDir(t)
	var log bytes.Buffer

	// 执行
	err := Up(context.Background(), Request{
		Target: Target{Dir: dir, ComposeFile: filepath.Base(file)},
		Log:    &log,
	})

	// 断言
	if err == nil {
		t.Fatalf("Up() error = nil, want failure; log=%s", log.String())
	}
	if !strings.Contains(err.Error(), "docker compose up -d failed") {
		t.Errorf("error = %v, want the compose prefix", err)
	}
	if !strings.Contains(err.Error(), "compose boom") {
		t.Errorf("error = %v, want the command output tail", err)
	}
}

func Test_Ps_when_CommandFails_thenReturnsOutputAndError(t *testing.T) {
	// 准备
	stubExec(t)
	t.Setenv("DEPLOY_HELPER_FAIL", "1")
	dir, file := targetDir(t)

	// 执行
	out, err := Ps(context.Background(), Request{
		Target: Target{Dir: dir, ComposeFile: filepath.Base(file)},
	})

	// 断言
	if err == nil {
		t.Fatal("Ps() error = nil, want failure")
	}
	if !strings.Contains(out, "compose boom") {
		t.Errorf("out = %q, want the command output", out)
	}
}

func Test_Up_when_DirMissing_thenClearError(t *testing.T) {
	// 准备
	missing := filepath.Join(t.TempDir(), "gone")

	// 执行
	err := Up(context.Background(), Request{
		Target: Target{Dir: missing, ComposeFile: "docker-compose.yaml"},
	})

	// 断言
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %v, want dir does not exist", err)
	}
}

func Test_Up_when_ComposeFileMissing_thenClearError(t *testing.T) {
	// 准备
	dir := t.TempDir()

	// 执行
	err := Up(context.Background(), Request{
		Target: Target{Dir: dir, ComposeFile: "docker-compose.yaml"},
	})

	// 断言
	if err == nil || !strings.Contains(err.Error(), "compose file") {
		t.Errorf("error = %v, want compose file missing", err)
	}
}

func Test_Up_when_Timeout_thenContextCancelled(t *testing.T) {
	// 准备：辅助进程会阻塞，直到上下文将其取消。
	stubExec(t)
	t.Setenv("DEPLOY_HELPER_SLEEP", "1")
	dir, file := targetDir(t)

	// 执行
	start := time.Now()
	err := Up(context.Background(), Request{
		Target:  Target{Dir: dir, ComposeFile: filepath.Base(file)},
		Timeout: 100 * time.Millisecond,
	})

	// 断言：resolve 已成功（dir/file 存在），并且运行已超时。
	if err == nil {
		t.Fatal("Up() error = nil, want timeout")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Up() took %v, want the 100ms timeout to bound it", elapsed)
	}
}
