package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

func TestHandleSessionAssetServesRelativeAndAbsoluteWorkDirImages(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	workDir := t.TempDir()
	imagePath := filepath.Join(workDir, "shots", "screen.png")
	writeTestPNG(t, imagePath)
	info := createAssetRouteSession(t, fs, workDir)

	for name, requestedPath := range map[string]string{
		"relative": "shots/screen.png",
		"absolute": imagePath,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+info.ID+"/asset?path="+url.QueryEscape(requestedPath), nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != "image/png" {
				t.Fatalf("Content-Type = %q, want image/png", got)
			}
			if !bytes.HasPrefix(rec.Body.Bytes(), pngHeader()) {
				t.Fatalf("served asset did not preserve PNG bytes")
			}
		})
	}
}

func TestHandleSessionAssetRejectsUnsafeOrUnsupportedFiles(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	parent := t.TempDir()
	workDir := filepath.Join(parent, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}
	info := createAssetRouteSession(t, fs, workDir)

	outside := filepath.Join(parent, "outside.png")
	writeTestPNG(t, outside)
	if err := os.WriteFile(filepath.Join(workDir, "note.png"), []byte("plain text"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if err := os.Mkdir(filepath.Join(workDir, "folder.png"), 0o755); err != nil {
		t.Fatalf("mkdir folder: %v", err)
	}
	oversized := filepath.Join(workDir, "huge.png")
	writeTestPNG(t, oversized)
	if err := os.Truncate(oversized, sessionAttachmentMaxBytes+1); err != nil {
		t.Fatalf("truncate oversized: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(workDir, "escaped.png")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "traversal", path: "../outside.png", want: http.StatusForbidden},
		{name: "symlink escape", path: "escaped.png", want: http.StatusForbidden},
		{name: "non image", path: "note.png", want: http.StatusUnsupportedMediaType},
		{name: "directory", path: "folder.png", want: http.StatusNotFound},
		{name: "missing", path: "missing.png", want: http.StatusNotFound},
		{name: "oversized", path: "huge.png", want: http.StatusRequestEntityTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+info.ID+"/asset?path="+url.QueryEscape(tc.path), nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestHandleSessionAssetRejectsSessionWithoutWorkDir(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)
	created, err := fs.cityBeadStore.Create(beads.Bead{
		Type:   session.BeadType,
		Title:  "No Workdir",
		Status: "open",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"provider":     "test",
			"session_name": "gc-no-workdir",
			"state":        string(session.StateActive),
			"template":     "default",
		},
	})
	if err != nil {
		t.Fatalf("create session bead: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/session/")+created.ID+"/asset?path=screen.png", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func createAssetRouteSession(t *testing.T, fs *fakeState, workDir string) session.Info {
	t.Helper()
	mgr := session.NewManager(fs.cityBeadStore, fs.sp)
	info, err := mgr.Create(context.Background(), "default", "Asset Test", "echo test", workDir, "test", nil, session.ProviderResume{}, runtime.Config{})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return info
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(pngHeader(), bytes.Repeat([]byte{0}, 64)...), 0o644); err != nil {
		t.Fatalf("write PNG: %v", err)
	}
}

func pngHeader() []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
}
