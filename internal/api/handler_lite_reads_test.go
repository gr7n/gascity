package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

type panicOnLiveProbeProvider struct {
	runtime.Provider
}

func (p panicOnLiveProbeProvider) fail(method string) {
	panic("lite read called live provider method: " + method)
}

func (p panicOnLiveProbeProvider) IsRunning(string) bool {
	p.fail("IsRunning")
	return false
}

func (p panicOnLiveProbeProvider) ProcessAlive(string, []string) bool {
	p.fail("ProcessAlive")
	return false
}

func (p panicOnLiveProbeProvider) GetMeta(string, string) (string, error) {
	p.fail("GetMeta")
	return "", nil
}

func (p panicOnLiveProbeProvider) IsAttached(string) bool {
	p.fail("IsAttached")
	return false
}

func (p panicOnLiveProbeProvider) GetLastActivity(string) (time.Time, error) {
	p.fail("GetLastActivity")
	return time.Time{}, nil
}

func (p panicOnLiveProbeProvider) ListRunning(string) ([]string, error) {
	p.fail("ListRunning")
	return nil, nil
}

func (p panicOnLiveProbeProvider) Peek(string, int) (string, error) {
	p.fail("Peek")
	return "", nil
}

type liteProbeState struct {
	*fakeState
	provider runtime.Provider
}

func (s *liteProbeState) SessionProvider() runtime.Provider {
	return s.provider
}

func TestLiteControlPlaneReadsDoNotProbeLiveProvider(t *testing.T) {
	base := newFakeState(t)
	state := &liteProbeState{
		fakeState: base,
		provider:  panicOnLiveProbeProvider{Provider: runtime.NewFake()},
	}
	h := newTestCityHandler(t, state)

	for _, path := range []string{
		"/status?lite=true",
		"/agents?lite=true",
		"/rigs?lite=true",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, cityURL(state, path), nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
		})
	}
}

func TestLiteSessionListDoesNotProbeLiveProvider(t *testing.T) {
	base := newSessionFakeState(t)
	info := createTestSession(t, base.cityBeadStore, base.sp, "Lite Session")
	state := &liteProbeState{
		fakeState: base,
		provider:  panicOnLiveProbeProvider{Provider: base.sp},
	}
	h := newTestCityHandler(t, state)

	for _, path := range []string{
		"/sessions?state=active&limit=10&lite=true&peek=true",
		"/sessions?state=active&limit=10&fresh=false&peek=true",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, cityURL(state, path), nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Items []sessionResponse `json:"items"`
				Total int               `json:"total"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Total != 1 || len(body.Items) != 1 {
				t.Fatalf("session list total/items = %d/%d, want 1/1", body.Total, len(body.Items))
			}
			got := body.Items[0]
			if got.ID != info.ID {
				t.Fatalf("session ID = %q, want %q", got.ID, info.ID)
			}
			if got.State != string(info.State) {
				t.Fatalf("session state = %q, want %q", got.State, info.State)
			}
			if !got.Running {
				t.Fatal("lite session Running = false, want true from stored active state")
			}
			if got.LastOutput != "" {
				t.Fatalf("lite session LastOutput = %q, want empty even when peek=true", got.LastOutput)
			}
			if !got.SubmissionCapabilities.SupportsInterruptNow {
				t.Fatalf("lite session submission capabilities = %+v, want interrupt support from metadata", got.SubmissionCapabilities)
			}
		})
	}
}
