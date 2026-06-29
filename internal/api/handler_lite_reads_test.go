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
