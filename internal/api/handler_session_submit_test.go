package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/nudgequeue"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

func TestHandleSessionSubmitDefaultsToProviderDefaultBehavior(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Submit Me")
	mgr := session.NewManagerWithOptions(fs.cityBeadStore, fs.sp)
	if err := mgr.Suspend(info.ID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted asyncAcceptedBody
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if accepted.RequestID == "" {
		t.Fatal("missing request_id")
	}

	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success == nil {
		t.Fatalf("session submit failed: %s: %s", failure.ErrorCode, failure.ErrorMessage)
	}
	// Default intent on a suspended session resumes immediately (not queued).
	if success.Queued {
		t.Fatalf("queued = true, want false (default intent resumes)")
	}
	if success.Intent != string(session.SubmitIntentDefault) {
		t.Fatalf("intent = %q, want %q", success.Intent, session.SubmitIntentDefault)
	}
}

func TestHandleSessionSubmitResultCanBePolledByRequestID(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Poll Me")
	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	accepted := decodeAsyncAccepted(t, rec.Body)
	if accepted.EventCursor == "" {
		t.Fatal("missing event_cursor")
	}

	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success == nil {
		t.Fatalf("session submit failed: %s: %s", failure.ErrorCode, failure.ErrorMessage)
	}

	statusReq := httptest.NewRequest(
		http.MethodGet,
		cityURL(fs, "/request/")+accepted.RequestID+"?after_seq="+accepted.EventCursor,
		nil,
	)
	statusRec := httptest.NewRecorder()
	h.ServeHTTP(statusRec, statusReq)

	if statusRec.Code != http.StatusOK {
		t.Fatalf("request status = %d, want %d; body: %s", statusRec.Code, http.StatusOK, statusRec.Body.String())
	}
	var status RequestStatus
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatalf("decode request status: %v", err)
	}
	if status.RequestID != accepted.RequestID || status.Status != requestStatusSucceeded {
		t.Fatalf("status = %#v, want succeeded for %s", status, accepted.RequestID)
	}
	if status.Operation != RequestOperationSessionSubmit {
		t.Fatalf("operation = %q, want %q", status.Operation, RequestOperationSessionSubmit)
	}
	if status.Stage != RequestStageSubmitted {
		t.Fatalf("stage = %q, want %q", status.Stage, RequestStageSubmitted)
	}
	if status.Progress == nil || status.Progress.Type != events.RequestProgress {
		t.Fatalf("progress = %#v, want latest request.progress", status.Progress)
	}
	if status.Event == nil || status.Event.Type != events.RequestResultSessionSubmit {
		t.Fatalf("event = %#v, want session submit result", status.Event)
	}
}

func TestHandleSessionSubmitRejectsWhenEventsUnavailable(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Submit Me")
	fs.eventProv = nil

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no_event_provider") {
		t.Fatalf("body = %s, want no_event_provider detail", rec.Body.String())
	}
}

func TestSessionCreateCommandableTimeoutAllowsSlowInteractiveStartup(t *testing.T) {
	if sessionCreateCommandableTimeout < 5*time.Minute {
		t.Fatalf("sessionCreateCommandableTimeout = %s, want at least 5m for slow provider startup", sessionCreateCommandableTimeout)
	}
}

func TestHandleSessionSubmitUsesImmediateDefaultForCodex(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	mgr := session.NewManagerWithOptions(fs.cityBeadStore, fs.sp)
	info, err := mgr.CreateSession(context.Background(), session.CreateOptions{Template: "helper", Title: "Codex Submit", Command: "codex", WorkDir: t.TempDir(), Provider: "codex", Env: nil, Resume: session.ProviderResume{}, Hints: runtime.Config{}, ExtraMeta: map[string]string{"session_origin": "manual"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mgr.Suspend(info.ID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted asyncAcceptedBody
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if accepted.RequestID == "" {
		t.Fatal("missing request_id")
	}

	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success == nil {
		t.Fatalf("session submit failed: %s: %s", failure.ErrorCode, failure.ErrorMessage)
	}
}

func TestHandleSessionSubmitFollowUpQueuesMessage(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Queue Me")

	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"later please","intent":"follow_up"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var accepted asyncAcceptedBody
	if err := json.NewDecoder(rec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if accepted.RequestID == "" {
		t.Fatal("missing request_id")
	}

	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success == nil {
		t.Fatalf("session submit failed: %s: %s", failure.ErrorCode, failure.ErrorMessage)
	}

	state, err := nudgequeue.LoadState(fs.cityPath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.Pending) != 1 {
		t.Fatalf("pending queued submits = %d, want 1", len(state.Pending))
	}
	item := state.Pending[0]
	if item.SessionID != info.ID {
		t.Fatalf("SessionID = %q, want %q", item.SessionID, info.ID)
	}
	if item.Message != "later please" {
		t.Fatalf("Message = %q, want %q", item.Message, "later please")
	}
}

func TestHandleSessionSubmitEmitsFailureWhenProviderNudgeHangs(t *testing.T) {
	fs := newSessionFakeState(t)
	blocker := &blockingNudgeProvider{
		Fake:    fs.sp,
		started: make(chan struct{}),
		unblock: make(chan struct{}),
	}
	t.Cleanup(func() {
		close(blocker.unblock)
	})
	prevTimeout := sessionSubmitAsyncTimeout
	sessionSubmitAsyncTimeout = 50 * time.Millisecond
	t.Cleanup(func() {
		sessionSubmitAsyncTimeout = prevTimeout
	})

	srv := New(&stateWithSessionProvider{fakeState: fs, provider: blocker})
	h := newTestCityHandlerWith(t, fs, srv)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "blocked-submit")
	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/submit", strings.NewReader(`{"message":"hello"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	accepted := decodeAsyncAccepted(t, rec.Body)

	select {
	case <-blocker.started:
	case <-time.After(testEventTimeout):
		t.Fatal("provider nudge was not reached")
	}
	progress := waitForRequestProgressStage(t, fs.eventProv, accepted.RequestID, RequestStageDelivering, testEventTimeout)
	if progress.SessionID != info.ID {
		t.Fatalf("progress session_id = %q, want %q", progress.SessionID, info.ID)
	}
	success, failure := waitForSessionSubmitResult(t, fs.eventProv, accepted.RequestID)
	if success != nil {
		t.Fatalf("unexpected success: %+v", success)
	}
	if failure == nil {
		t.Fatal("expected request.failed for blocked provider nudge")
	}
	if failure.ErrorCode != "timeout" {
		t.Fatalf("failure error_code = %q, want timeout", failure.ErrorCode)
	}
	if failure.Stage != RequestStageDelivering {
		t.Fatalf("failure stage = %q, want %q", failure.Stage, RequestStageDelivering)
	}
	if !strings.Contains(failure.ErrorMessage, "stage="+RequestStageDelivering) {
		t.Fatalf("failure error_message = %q, want stage diagnostic", failure.ErrorMessage)
	}
}

func waitForRequestProgressStage(t *testing.T, prov events.Provider, requestID, stage string, timeout time.Duration) *RequestProgressPayload {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		progressEvents, _ := prov.List(events.Filter{Type: events.RequestProgress})
		for _, e := range progressEvents {
			var p RequestProgressPayload
			if json.Unmarshal(e.Payload, &p) == nil && requestIDMatches(p.RequestID, requestID) && p.Stage == stage {
				return &p
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for request.progress stage=%q request_id=%q", stage, requestID)
	return nil
}

func TestSessionSubmitAsyncTimeoutIsOperatorBounded(t *testing.T) {
	if sessionSubmitAsyncTimeout <= 0 || sessionSubmitAsyncTimeout > 30*time.Second {
		t.Fatalf("sessionSubmitAsyncTimeout = %s, want <= 30s", sessionSubmitAsyncTimeout)
	}
	if sessionSubmitAsyncTimeout >= sessionMessageTimeout {
		t.Fatalf("sessionSubmitAsyncTimeout = %s, want shorter than client timeout %s", sessionSubmitAsyncTimeout, sessionMessageTimeout)
	}
}

func TestHandleSessionGetIncludesSubmissionCapabilities(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	info := createTestSession(t, fs.cityBeadStore, fs.sp, "Capabilities")
	if err := fs.cityBeadStore.Update(info.ID, beads.UpdateOpts{
		Metadata: map[string]string{
			"pool_managed": "true",
			"pool_slot":    "1",
		},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", cityURL(fs, "/session/")+info.ID, nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp sessionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.SubmissionCapabilities.SupportsFollowUp {
		t.Fatal("SupportsFollowUp = false, want true")
	}
	if !resp.SubmissionCapabilities.SupportsInterruptNow {
		t.Fatal("SupportsInterruptNow = false, want true")
	}
}

func TestHandleSessionStopUsesSoftEscapeForCodex(t *testing.T) {
	fs := newSessionFakeState(t)
	h := newTestCityHandler(t, fs)

	mgr := session.NewManagerWithOptions(fs.cityBeadStore, fs.sp)
	info, err := mgr.CreateSession(context.Background(), session.CreateOptions{Template: "helper", Title: "Codex", Command: "codex", WorkDir: t.TempDir(), Provider: "codex", Env: nil, Resume: session.ProviderResume{}, Hints: runtime.Config{}, ExtraMeta: map[string]string{"session_origin": "manual"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := fs.cityBeadStore.Update(info.ID, beads.UpdateOpts{
		Metadata: map[string]string{"pool_managed": "true"},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	rec := httptest.NewRecorder()
	req := newPostRequest(cityURL(fs, "/session/")+info.ID+"/stop", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var sawEscape, sawInterrupt bool
	for _, call := range fs.sp.Calls {
		if call.Method == "SendKeys" && call.Name == info.SessionName && call.Message == "Escape" {
			sawEscape = true
		}
		if call.Method == "Interrupt" && call.Name == info.SessionName {
			sawInterrupt = true
		}
	}
	if !sawEscape {
		t.Fatalf("calls = %#v, want SendKeys(Escape)", fs.sp.Calls)
	}
	if sawInterrupt {
		t.Fatalf("calls = %#v, did not want Interrupt for codex stop", fs.sp.Calls)
	}
}
