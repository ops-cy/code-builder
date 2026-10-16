package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab-builder/internal/config"
	"gitlab-builder/internal/queue"
	"gitlab-builder/internal/store"
	"gitlab-builder/internal/webhook"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.New(t.TempDir())
	deps := webhook.Deps{
		Repos:     nil,
		Secret:    "x",
		Store:     st,
		Queue:     queue.New(1, 1),
		Logger:    logger,
		Workspace: t.TempDir(),
	}
	cfg := config.Config{Server: config.ServerConfig{Listen: ":0"}}
	return New(cfg, logger, deps), st
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func Test_Healthz_when_Called_thenReturn200OK(t *testing.T) {
	// Given
	s, _ := newTestServer(t)

	// When
	w := get(t, s, "/healthz")

	// Then
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Errorf("body = %s, want ok true", w.Body.String())
	}
}

func Test_ListBuilds_when_Empty_thenReturnEmptyArray(t *testing.T) {
	// Given
	s, _ := newTestServer(t)

	// When
	w := get(t, s, "/api/builds")

	// Then
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("body = %q, want []", w.Body.String())
	}
}

func Test_GetBuild_when_UnknownID_thenReturn404(t *testing.T) {
	// Given
	s, _ := newTestServer(t)

	// When
	w := get(t, s, "/api/builds/bogus")

	// Then
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func Test_GetBuild_when_KnownID_thenReturnBuildJSON(t *testing.T) {
	// Given
	s, st := newTestServer(t)
	created := st.Create(store.Build{Repo: "app", Branch: "main", SHA: "abc", Status: store.StatusQueued})

	// When
	w := get(t, s, "/api/builds/"+created.ID)

	// Then
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got store.Build
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != created.ID || got.Repo != "app" {
		t.Errorf("got = %+v, want created build", got)
	}
}

func Test_BuildLog_when_UnknownID_thenReturn404(t *testing.T) {
	// Given
	s, _ := newTestServer(t)

	// When
	w := get(t, s, "/api/builds/bogus/log")

	// Then
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func Test_BuildLog_when_Written_thenReturnContent(t *testing.T) {
	// Given
	s, st := newTestServer(t)
	created := st.Create(store.Build{Repo: "app"})
	f, err := st.OpenLog(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("line1\nline2\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// When
	w := get(t, s, "/api/builds/"+created.ID+"/log")

	// Then
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "line1\nline2\n" {
		t.Errorf("body = %q, want log content", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q, want text/plain", ct)
	}
}
