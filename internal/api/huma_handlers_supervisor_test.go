package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/cityinit"
	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/events"
)

type fakeInitializer struct {
	scaffoldReq    cityinit.InitRequest
	scaffoldResult *cityinit.InitResult
	scaffoldErr    error

	findName   string
	findResult cityinit.RegisteredCity
	findErr    error

	unregisterReq    cityinit.UnregisterRequest
	unregisterResult *cityinit.UnregisterResult
	unregisterErr    error
}

func (f *fakeInitializer) Init(context.Context, cityinit.InitRequest) (*cityinit.InitResult, error) {
	return nil, errors.New("Init should not be called by supervisor tests")
}

func (f *fakeInitializer) Scaffold(_ context.Context, req cityinit.InitRequest) (*cityinit.InitResult, error) {
	f.scaffoldReq = req
	if f.scaffoldErr != nil {
		return f.scaffoldResult, f.scaffoldErr
	}
	return f.scaffoldResult, nil
}

func (f *fakeInitializer) FindRegisteredCity(_ context.Context, name string) (cityinit.RegisteredCity, error) {
	f.findName = name
	if f.findErr != nil {
		return cityinit.RegisteredCity{}, f.findErr
	}
	if f.findResult.Name == "" && f.findResult.Path == "" {
		return cityinit.RegisteredCity{}, cityinit.ErrNotRegistered
	}
	return f.findResult, nil
}

func (f *fakeInitializer) Unregister(_ context.Context, req cityinit.UnregisterRequest) (*cityinit.UnregisterResult, error) {
	f.unregisterReq = req
	if f.unregisterErr != nil {
		return nil, f.unregisterErr
	}
	return f.unregisterResult, nil
}

func newTestSupervisorMuxWithInitializer(t *testing.T, init cityInitializer) *SupervisorMux {
	t.Helper()
	return NewSupervisorMux(&fakeCityResolver{
		cities:             map[string]*fakeState{},
		supervisorRecorder: events.NewFake(),
	}, init, false, "test", "", time.Now())
}

func TestSupervisorCityCreateConflictsWhenTargetAlreadyInitialized(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{
			name: "scaffold_present",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				for _, path := range []string{
					filepath.Join(dir, citylayout.RuntimeRoot),
					filepath.Join(dir, citylayout.RuntimeRoot, "cache"),
					filepath.Join(dir, citylayout.RuntimeRoot, "runtime"),
					filepath.Join(dir, citylayout.RuntimeRoot, "system"),
				} {
					if err := os.MkdirAll(path, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, citylayout.RuntimeRoot, "events.jsonl"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "city")
			tc.setup(t, dir)

			sm := newTestSupervisorMux(t, map[string]*fakeState{})
			req := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{"dir":"`+dir+`","provider":"claude"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-GC-Request", "test")
			rec := httptest.NewRecorder()

			sm.ServeHTTP(rec, req)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "already initialized") {
				t.Fatalf("body = %q, want already initialized detail", rec.Body.String())
			}
		})
	}
}

func TestSupervisorCityCreateScaffoldsViaInitializer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))
	cityPath := filepath.Join(home, "mc-city")
	init := &fakeInitializer{
		scaffoldResult: &cityinit.InitResult{
			CityName:      "mc-city",
			CityPath:      cityPath,
			ProviderUsed:  "codex",
			ReloadWarning: "reload failed",
		},
	}
	sm := newTestSupervisorMuxWithInitializer(t, init)

	req := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{
		"dir":"mc-city",
		"provider":"codex",
		"bootstrap_profile":"single-host-compat"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if init.scaffoldReq.Dir != cityPath {
		t.Fatalf("Scaffold Dir = %q, want %q", init.scaffoldReq.Dir, cityPath)
	}
	if init.scaffoldReq.Provider != "codex" || init.scaffoldReq.BootstrapProfile != "single-host-compat" {
		t.Fatalf("Scaffold request = %+v, want codex + single-host-compat", init.scaffoldReq)
	}
	if !init.scaffoldReq.SkipProviderReadiness {
		t.Fatal("Scaffold request should skip provider readiness for API callers")
	}
	if body := rec.Body.String(); !strings.Contains(body, `"request_id"`) {
		t.Fatalf("body = %s, want request_id", body)
	}
}

func TestSupervisorCityCreateScaffoldsWithStartCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))
	cityPath := filepath.Join(home, "mc-city")
	init := &fakeInitializer{
		scaffoldResult: &cityinit.InitResult{
			CityName:     "mc-city",
			CityPath:     cityPath,
			ProviderUsed: "",
		},
	}
	sm := newTestSupervisorMuxWithInitializer(t, init)

	req := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{
		"dir":"mc-city",
		"start_command":"bash /tmp/hermetic-agent.sh"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if init.scaffoldReq.Dir != cityPath {
		t.Fatalf("Scaffold Dir = %q, want %q", init.scaffoldReq.Dir, cityPath)
	}
	if init.scaffoldReq.Provider != "" || init.scaffoldReq.StartCommand != "bash /tmp/hermetic-agent.sh" {
		t.Fatalf("Scaffold request = %+v, want start_command without provider", init.scaffoldReq)
	}
	if !init.scaffoldReq.SkipProviderReadiness {
		t.Fatal("Scaffold request should skip provider readiness for API callers")
	}
}

func TestSupervisorCityCreateReturnsRequestID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))
	cityPath := filepath.Join(home, "mc-city")
	init := &fakeInitializer{
		scaffoldResult: &cityinit.InitResult{
			CityName:     "mc-city",
			CityPath:     cityPath,
			ProviderUsed: "codex",
		},
	}
	sm := newTestSupervisorMuxWithInitializer(t, init)

	req := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{
		"dir":"mc-city",
		"provider":"codex",
		"bootstrap_profile":"single-host-compat"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"request_id"`) {
		t.Fatalf("response must include request_id for async correlation; body=%s", body)
	}
}

func TestSupervisorCityCreateReturnsCurrentEventCursor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))
	cityPath := filepath.Join(home, "mc-city")
	recorder := events.NewFake()
	recorder.Record(events.Event{Type: events.SessionWoke, Actor: "seed"})
	resolver := &fakeCityResolver{
		cities:             map[string]*fakeState{},
		supervisorRecorder: recorder,
	}
	init := &fakeInitializer{
		scaffoldResult: &cityinit.InitResult{
			CityName:     "mc-city",
			CityPath:     cityPath,
			ProviderUsed: "codex",
		},
	}
	sm := NewSupervisorMux(resolver, init, false, "test", "", time.Now())

	req := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{"dir":"mc-city","provider":"codex"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	accepted := decodeAsyncAccepted(t, rec.Body)
	if accepted.EventCursor != "__supervisor__:1" {
		t.Fatalf("event_cursor = %q, want __supervisor__:1", accepted.EventCursor)
	}
}

func TestSupervisorCityCreateStoresPendingRequestForReconciler(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GC_HOME", filepath.Join(home, ".gc"))
	cityPath := filepath.Join(home, "mc-city")
	resolver := &fakeCityResolver{
		cities:             map[string]*fakeState{},
		supervisorRecorder: events.NewFake(),
	}
	init := &fakeInitializer{
		scaffoldResult: &cityinit.InitResult{
			CityName:     "mc-city",
			CityPath:     cityPath,
			ProviderUsed: "claude",
		},
	}
	sm := NewSupervisorMux(resolver, init, false, "test", "", time.Now())

	postReq := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{"dir":"mc-city","provider":"claude"}`))
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("X-GC-Request", "test")
	postRec := httptest.NewRecorder()

	sm.ServeHTTP(postRec, postReq)

	if postRec.Code != http.StatusAccepted {
		t.Fatalf("POST /v0/city status = %d, want %d; body=%s", postRec.Code, http.StatusAccepted, postRec.Body.String())
	}
	var createResp struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(postRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v; body=%s", err, postRec.Body.String())
	}
	if createResp.RequestID == "" {
		t.Fatalf("empty request_id in response; body=%s", postRec.Body.String())
	}
	if got := resolver.pending[cityPath]; got != createResp.RequestID {
		t.Fatalf("pending request_id = %q, want %q", got, createResp.RequestID)
	}
	if got := len(resolver.supervisorRecorder.(*events.Fake).Events); got != 0 {
		t.Fatalf("supervisor events = %d, want 0 before reconciler starts city", got)
	}
}

func TestSupervisorCityCreateRejectsDuplicatePendingRequest(t *testing.T) {
	cityPath := filepath.Join(t.TempDir(), "mc-city")
	resolver := &fakeCityResolver{
		cities:             map[string]*fakeState{},
		pending:            map[string]string{cityPath: "req-existing"},
		supervisorRecorder: events.NewFake(),
	}
	init := &fakeInitializer{
		scaffoldResult: &cityinit.InitResult{
			CityName:     "mc-city",
			CityPath:     cityPath,
			ProviderUsed: "claude",
		},
	}
	sm := NewSupervisorMux(resolver, init, false, "test", "", time.Now())

	req := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{"dir":"`+cityPath+`","provider":"claude"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if got := resolver.pending[cityPath]; got != "req-existing" {
		t.Fatalf("pending request_id = %q, want req-existing", got)
	}
	if init.scaffoldReq.Dir != "" {
		t.Fatalf("Scaffold was called despite duplicate pending request: %+v", init.scaffoldReq)
	}
}

func TestSupervisorCityCreateEmitsFailedEventForPostRegisterFailure(t *testing.T) {
	cityPath := filepath.Join(t.TempDir(), "mc-city")
	resolver := &fakeCityResolver{
		cities:             map[string]*fakeState{},
		supervisorRecorder: events.NewFake(),
	}
	lifecycleErr := errors.New("record city created event: disk full")
	init := &fakeInitializer{
		scaffoldResult: &cityinit.InitResult{
			CityName:     "mc-city",
			CityPath:     cityPath,
			ProviderUsed: "claude",
		},
		scaffoldErr: cityinit.NewPostRegisterFailure(lifecycleErr),
	}
	sm := NewSupervisorMux(resolver, init, false, "test", "", time.Now())

	req := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{"dir":"`+cityPath+`","provider":"claude"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	accepted := decodeAsyncAccepted(t, rec.Body)
	if _, ok, err := resolver.ConsumePendingRequestID(cityPath); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("pending request_id survived post-register failure")
	}
	recorded := resolver.supervisorRecorder.(*events.Fake).Events
	if len(recorded) != 1 {
		t.Fatalf("recorded %d events, want 1", len(recorded))
	}
	if recorded[0].Type != events.RequestFailed {
		t.Fatalf("event type = %q, want %q", recorded[0].Type, events.RequestFailed)
	}
	var payload RequestFailedPayload
	if err := json.Unmarshal(recorded[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.RequestID != accepted.RequestID {
		t.Fatalf("request_id = %q, want %q", payload.RequestID, accepted.RequestID)
	}
	if payload.Operation != RequestOperationCityCreate {
		t.Fatalf("operation = %q, want %q", payload.Operation, RequestOperationCityCreate)
	}
	if payload.ErrorCode != "city_init_failed" {
		t.Fatalf("error_code = %q, want city_init_failed", payload.ErrorCode)
	}
	if !strings.Contains(payload.ErrorMessage, lifecycleErr.Error()) {
		t.Fatalf("error_message = %q, want %q", payload.ErrorMessage, lifecycleErr.Error())
	}
}

func TestSupervisorCityRequestResultUsesCityTagOnSupervisorStream(t *testing.T) {
	resolver := &fakeCityResolver{
		cities:             map[string]*fakeState{},
		supervisorRecorder: events.NewFake(),
	}
	sm := NewSupervisorMux(resolver, nil, false, "test", "", time.Now())

	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	streamReq := httptest.NewRequest(http.MethodGet, "/v0/events/stream?after_cursor=0", nil).WithContext(streamCtx)
	streamReq.Header.Set("Accept", "text/event-stream")
	streamRec := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		sm.ServeHTTP(streamRec, streamReq)
	}()

	time.Sleep(50 * time.Millisecond)
	EmitTypedEvent(resolver.supervisorRecorder, events.RequestResultCityCreate, "mc-city", CityCreateSucceededPayload{
		RequestID: "req-test",
		Name:      "mc-city",
		Path:      "/tmp/mc-city",
	})

	time.Sleep(250 * time.Millisecond)
	cancelStream()
	<-streamDone

	if streamRec.Code != http.StatusOK {
		t.Fatalf("GET /v0/events/stream status = %d, want %d; body=%s", streamRec.Code, http.StatusOK, streamRec.Body.String())
	}

	frames := parseSSETestFrames(streamRec.Body.String())
	observed := make([]string, 0, len(frames))
	for _, frame := range frames {
		if frame.Data == "" {
			continue
		}
		var env struct {
			Type    string         `json:"type"`
			City    string         `json:"city"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal([]byte(frame.Data), &env); err != nil {
			t.Fatalf("decode SSE data: %v; data=%s", err, frame.Data)
		}
		observed = append(observed, env.Type)
		if env.Payload["request_id"] != "req-test" {
			continue
		}
		switch env.Type {
		case events.RequestResultCityCreate:
			if env.City != "mc-city" {
				t.Fatalf("city tag = %q, want mc-city; frame=%s", env.City, frame.Data)
			}
			return
		case events.RequestFailed:
			t.Fatalf("city create emitted request.failed for request_id req-test: %s", frame.Data)
		}
	}
	t.Fatalf("stream did not emit request.result.city.create for request_id req-test; observed event types=%v body=%s", observed, streamRec.Body.String())
}

func TestSupervisorRequestStatusPending(t *testing.T) {
	resolver := &fakeCityResolver{
		cities:             map[string]*fakeState{},
		supervisorRecorder: events.NewFake(),
	}
	sm := NewSupervisorMux(resolver, nil, false, "test", "", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/v0/request/req-missing?after_cursor=__supervisor__:0", nil)
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp SupervisorRequestStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if resp.RequestID != "req-missing" || resp.Status != requestStatusPending || resp.Event != nil {
		t.Fatalf("response = %+v, want pending missing request", resp)
	}
}

func TestSupervisorRequestStatusSucceededAfterCursor(t *testing.T) {
	recorder := events.NewFake()
	recorder.Record(events.Event{Type: events.SessionWoke, Actor: "seed"})
	EmitTypedEvent(recorder, events.RequestResultCityCreate, "mc-city", CityCreateSucceededPayload{
		RequestID: "req-city",
		Name:      "mc-city",
		Path:      "/tmp/mc-city",
	})
	resolver := &fakeCityResolver{
		cities:             map[string]*fakeState{},
		supervisorRecorder: recorder,
	}
	sm := NewSupervisorMux(resolver, nil, false, "test", "", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/v0/request/req-city?after_cursor=__supervisor__:1", nil)
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp SupervisorRequestStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if resp.Status != requestStatusSucceeded || resp.Operation != RequestOperationCityCreate {
		t.Fatalf("response = %+v, want city.create succeeded", resp)
	}
	if resp.Event == nil || resp.Event.Type != events.RequestResultCityCreate || resp.Event.City != "mc-city" {
		t.Fatalf("terminal event = %+v, want tagged mc-city create result", resp.Event)
	}
}

func TestSupervisorRequestStatusAfterCursorExcludesSeenResult(t *testing.T) {
	recorder := events.NewFake()
	EmitTypedEvent(recorder, events.RequestResultCityCreate, "mc-city", CityCreateSucceededPayload{
		RequestID: "req-city",
		Name:      "mc-city",
		Path:      "/tmp/mc-city",
	})
	resolver := &fakeCityResolver{
		cities:             map[string]*fakeState{},
		supervisorRecorder: recorder,
	}
	sm := NewSupervisorMux(resolver, nil, false, "test", "", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/v0/request/req-city?after_cursor=__supervisor__:1", nil)
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp SupervisorRequestStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if resp.Status != requestStatusPending || resp.Event != nil {
		t.Fatalf("response = %+v, want pending because cursor already passed result", resp)
	}
}

func TestSupervisorRequestStatusReadsCityProviderResults(t *testing.T) {
	state := newFakeState(t)
	state.cityName = "alpha"
	ep := state.eventProv.(*events.Fake)
	ep.Record(events.Event{Type: events.SessionWoke, Actor: "seed"})
	recordPayloadEvent(t, ep, events.RequestResultSessionSubmit, "director", SessionSubmitSucceededPayload{
		RequestID: "req-submit",
		SessionID: "director",
		Queued:    false,
		Intent:    "default",
	})

	sm := newTestSupervisorMux(t, map[string]*fakeState{"alpha": state})
	req := httptest.NewRequest(http.MethodGet, "/v0/request/req-submit?after_cursor=alpha:1", nil)
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp SupervisorRequestStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if resp.Status != requestStatusSucceeded || resp.Operation != RequestOperationSessionSubmit {
		t.Fatalf("response = %+v, want session.submit succeeded", resp)
	}
	if resp.Event == nil || resp.Event.City != "alpha" || resp.Event.Type != events.RequestResultSessionSubmit {
		t.Fatalf("terminal event = %+v, want tagged alpha submit result", resp.Event)
	}
}

func TestSupervisorRequestStatusPrefersRequestEventProvider(t *testing.T) {
	state := newFakeState(t)
	state.cityName = "alpha"
	ep := state.eventProv.(*events.Fake)
	ep.Record(events.Event{Type: events.SessionWoke, Actor: "seed"})
	recordPayloadEvent(t, ep, events.RequestResultSessionSubmit, "director", SessionSubmitSucceededPayload{
		RequestID: "req-submit",
		SessionID: "director",
		Queued:    false,
		Intent:    "default",
	})
	recorder := &indexedRecordingEventProvider{recordingEventProvider: &recordingEventProvider{Provider: ep}}
	state.eventProv = recorder

	sm := newTestSupervisorMux(t, map[string]*fakeState{"alpha": state})
	req := httptest.NewRequest(http.MethodGet, "/v0/request/req-submit?after_cursor=alpha:1", nil)
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp SupervisorRequestStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if resp.Status != requestStatusSucceeded || resp.Event == nil || resp.Event.City != "alpha" {
		t.Fatalf("response = %+v, want alpha indexed session.submit success", resp)
	}
	if len(recorder.requestLookups) != 1 || recorder.requestLookups[0].requestID != "req-submit" || recorder.requestLookups[0].afterSeq != 1 {
		t.Fatalf("request lookups = %#v, want alpha req-submit after seq 1", recorder.requestLookups)
	}
	if len(recorder.filters) != 0 || len(recorder.tailFilters) != 0 {
		t.Fatalf("supervisor request status fell back to general scans: filters=%#v tail=%#v", recorder.filters, recorder.tailFilters)
	}
}

func TestSupervisorRequestStatusUsesBoundedTailWithoutCursor(t *testing.T) {
	state := newFakeState(t)
	state.cityName = "alpha"
	ep := state.eventProv.(*events.Fake)
	for i := 0; i < 20; i++ {
		recordPayloadEvent(t, ep, events.RequestProgress, "noise", RequestProgressPayload{
			RequestID: "req-noise",
			Operation: RequestOperationSessionSubmit,
			Stage:     RequestStageDelivering,
		})
	}
	recordPayloadEvent(t, ep, events.RequestResultSessionSubmit, "director", SessionSubmitSucceededPayload{
		RequestID: "req-submit",
		SessionID: "director",
		Queued:    false,
		Intent:    "default",
	})
	recorder := &recordingEventProvider{Provider: ep}
	state.eventProv = recorder

	sm := newTestSupervisorMux(t, map[string]*fakeState{"alpha": state})
	req := httptest.NewRequest(http.MethodGet, "/v0/request/req-submit", nil)
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp SupervisorRequestStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if resp.Status != requestStatusSucceeded || resp.Event == nil || resp.Event.City != "alpha" {
		t.Fatalf("response = %+v, want alpha session.submit success", resp)
	}
	if len(recorder.filters) != 0 {
		t.Fatalf("cursorless supervisor request status used full list filters: %#v", recorder.filters)
	}
	if len(recorder.tailFilters) != 1 {
		t.Fatalf("tail filters = %#v, want one bounded tail lookup", recorder.tailFilters)
	}
	if got := recorder.tailLimits[0]; got != requestStatusTailScanLimit {
		t.Fatalf("tail limit = %d, want %d", got, requestStatusTailScanLimit)
	}
}

func TestSupervisorCityCreateMapsInitializerErrors(t *testing.T) {
	cityPath := filepath.Join(t.TempDir(), "mc-city")
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "already initialized", err: cityinit.ErrAlreadyInitialized, want: http.StatusConflict},
		{name: "invalid directory", err: cityinit.ErrInvalidDirectory, want: http.StatusUnprocessableEntity},
		{name: "invalid provider", err: cityinit.ErrInvalidProvider, want: http.StatusUnprocessableEntity},
		{name: "invalid bootstrap", err: cityinit.ErrInvalidBootstrapProfile, want: http.StatusUnprocessableEntity},
		{name: "generic", err: errors.New("boom"), want: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			init := &fakeInitializer{scaffoldErr: tc.err}
			sm := newTestSupervisorMuxWithInitializer(t, init)
			req := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{"dir":"`+cityPath+`","provider":"codex"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-GC-Request", "test")
			rec := httptest.NewRecorder()

			sm.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestSupervisorCityCreateClearsPendingRequestOnScaffoldError(t *testing.T) {
	cityPath := filepath.Join(t.TempDir(), "mc-city")
	resolver := &fakeCityResolver{cities: map[string]*fakeState{}, supervisorRecorder: events.NewFake()}
	init := &fakeInitializer{scaffoldErr: errors.New("scaffold failed")}
	sm := NewSupervisorMux(resolver, init, false, "test", "", time.Now())
	req := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{"dir":"`+cityPath+`","provider":"codex"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if _, ok, err := resolver.ConsumePendingRequestID(cityPath); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("pending request_id for %q survived synchronous scaffold failure", cityPath)
	}
}

func TestSupervisorCityCreateWithoutInitializerReturns501(t *testing.T) {
	sm := newTestSupervisorMux(t, map[string]*fakeState{})
	cityPath := filepath.Join(t.TempDir(), "mc-city")
	req := httptest.NewRequest(http.MethodPost, "/v0/city", strings.NewReader(`{"dir":"`+cityPath+`","provider":"codex"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
}

func TestSupervisorCityUnregisterUsesInitializer(t *testing.T) {
	init := &fakeInitializer{
		unregisterResult: &cityinit.UnregisterResult{
			CityName:      "mc-city",
			CityPath:      "/tmp/mc-city",
			ReloadWarning: "reload failed",
		},
	}
	sm := newTestSupervisorMuxWithInitializer(t, init)
	req := httptest.NewRequest(http.MethodPost, "/v0/city/mc-city/unregister", nil)
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if init.unregisterReq.CityName != "mc-city" {
		t.Fatalf("Unregister CityName = %q, want mc-city", init.unregisterReq.CityName)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"request_id"`) {
		t.Fatalf("body = %s, want request_id", body)
	}
}

func TestSupervisorCityUnregisterReturnsCurrentEventCursor(t *testing.T) {
	recorder := events.NewFake()
	recorder.Record(events.Event{Type: events.SessionWoke, Actor: "seed"})
	resolver := &fakeCityResolver{
		cities:             map[string]*fakeState{},
		supervisorRecorder: recorder,
	}
	init := &fakeInitializer{
		unregisterResult: &cityinit.UnregisterResult{
			CityName: "mc-city",
			CityPath: "/tmp/mc-city",
		},
	}
	sm := NewSupervisorMux(resolver, init, false, "test", "", time.Now())
	req := httptest.NewRequest(http.MethodPost, "/v0/city/mc-city/unregister", nil)
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	accepted := decodeAsyncAccepted(t, rec.Body)
	if accepted.EventCursor != "__supervisor__:1" {
		t.Fatalf("event_cursor = %q, want __supervisor__:1", accepted.EventCursor)
	}
}

func TestSupervisorCityUnregisterStoresPendingRequestFromRegistryWhenSnapshotMissing(t *testing.T) {
	const cityPath = "/tmp/mc-city"
	resolver := &fakeCityResolver{
		cities:             map[string]*fakeState{},
		supervisorRecorder: events.NewFake(),
	}
	init := &fakeInitializer{
		findResult: cityinit.RegisteredCity{
			Name: "mc-city",
			Path: cityPath,
		},
		unregisterResult: &cityinit.UnregisterResult{
			CityName: "mc-city",
			CityPath: cityPath,
		},
	}
	sm := NewSupervisorMux(resolver, init, false, "test", "", time.Now())
	req := httptest.NewRequest(http.MethodPost, "/v0/city/mc-city/unregister", nil)
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if init.findName != "mc-city" {
		t.Fatalf("FindRegisteredCity name = %q, want mc-city", init.findName)
	}
	if got := resolver.pending[cityPath]; got == "" {
		t.Fatalf("pending request_id for %q was not stored", cityPath)
	}
}

func TestSupervisorCityUnregisterMapsNotRegistered(t *testing.T) {
	init := &fakeInitializer{unregisterErr: cityinit.ErrNotRegistered}
	sm := newTestSupervisorMuxWithInitializer(t, init)
	req := httptest.NewRequest(http.MethodPost, "/v0/city/missing/unregister", nil)
	req.Header.Set("X-GC-Request", "test")
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestCityDirAlreadyInitializedAllowsConfigOnlyBootstrap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, citylayout.CityConfigFile), []byte("[workspace]\nname = \"alpha\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if cityDirAlreadyInitialized(dir) {
		t.Fatal("config-only city should be left for gc init bootstrap")
	}
}
