package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gastownhall/gascity/internal/events"
)

func TestRequestStatusPending(t *testing.T) {
	state := newFakeState(t)
	h := newTestCityHandler(t, state)

	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/request/req-missing"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp RequestStatus
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RequestID != "req-missing" || resp.Status != requestStatusPending || resp.Event != nil {
		t.Fatalf("response = %#v, want pending req-missing without event", resp)
	}
}

func TestRequestStatusReportsLatestProgress(t *testing.T) {
	state := newFakeState(t)
	ep := state.eventProv.(*events.Fake)
	recordPayloadEvent(t, ep, events.RequestProgress, "director", RequestProgressPayload{
		RequestID:     "req-progress",
		Operation:     RequestOperationSessionSubmit,
		Stage:         RequestStageResolving,
		SessionTarget: "director",
		ElapsedMs:     1,
	})
	recordPayloadEvent(t, ep, events.RequestProgress, "s-gc-1", RequestProgressPayload{
		RequestID:     "req-progress",
		Operation:     RequestOperationSessionSubmit,
		Stage:         RequestStageDelivering,
		SessionTarget: "director",
		SessionID:     "s-gc-1",
		ElapsedMs:     25,
	})
	h := newTestCityHandler(t, state)

	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/request/req-progress"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp RequestStatus
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != requestStatusPending || resp.Operation != RequestOperationSessionSubmit || resp.Stage != RequestStageDelivering {
		t.Fatalf("response = %#v, want pending session.submit at delivering", resp)
	}
	if resp.Progress == nil || resp.Progress.Type != events.RequestProgress || resp.Progress.Subject != "s-gc-1" {
		t.Fatalf("progress = %#v, want latest request.progress for s-gc-1", resp.Progress)
	}
	if resp.Event != nil {
		t.Fatalf("event = %#v, want no terminal event", resp.Event)
	}
}

func TestRequestStatusSucceeded(t *testing.T) {
	state := newFakeState(t)
	ep := state.eventProv.(*events.Fake)
	recordPayloadEvent(t, ep, events.RequestResultSessionSubmit, "worker", SessionSubmitSucceededPayload{
		RequestID: "req-old",
		SessionID: "worker",
	})
	recordPayloadEvent(t, ep, events.RequestResultSessionSubmit, "director", SessionSubmitSucceededPayload{
		RequestID: "req-want",
		SessionID: "director",
		Queued:    true,
		Intent:    "default",
	})
	h := newTestCityHandler(t, state)

	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/request/req-want"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp RequestStatus
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != requestStatusSucceeded || resp.Operation != RequestOperationSessionSubmit {
		t.Fatalf("response = %#v, want session.submit success", resp)
	}
	if resp.Event == nil || resp.Event.Type != events.RequestResultSessionSubmit || resp.Event.Subject != "director" {
		t.Fatalf("event = %#v, want director session submit result", resp.Event)
	}
}

func TestRequestStatusFailed(t *testing.T) {
	state := newFakeState(t)
	ep := state.eventProv.(*events.Fake)
	recordPayloadEvent(t, ep, events.RequestFailed, "", RequestFailedPayload{
		RequestID:    "req-fail",
		Operation:    RequestOperationSessionSubmit,
		ErrorCode:    "timeout",
		ErrorMessage: "session.submit timed out",
		Stage:        RequestStageDelivering,
		ElapsedMs:    50,
	})
	h := newTestCityHandler(t, state)

	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/request/req-fail"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp RequestStatus
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != requestStatusFailed || resp.Operation != RequestOperationSessionSubmit {
		t.Fatalf("response = %#v, want session.submit failure", resp)
	}
	if resp.Stage != RequestStageDelivering {
		t.Fatalf("stage = %q, want %q", resp.Stage, RequestStageDelivering)
	}
	if resp.Event == nil || resp.Event.Type != events.RequestFailed {
		t.Fatalf("event = %#v, want request.failed", resp.Event)
	}
}

func TestRequestStatusTerminalRetainsLatestProgress(t *testing.T) {
	state := newFakeState(t)
	ep := state.eventProv.(*events.Fake)
	recordPayloadEvent(t, ep, events.RequestProgress, "director", RequestProgressPayload{
		RequestID:     "req-done",
		Operation:     RequestOperationSessionSubmit,
		Stage:         RequestStageDelivering,
		SessionTarget: "director",
		SessionID:     "director",
		ElapsedMs:     10,
	})
	recordPayloadEvent(t, ep, events.RequestProgress, "director", RequestProgressPayload{
		RequestID:     "req-done",
		Operation:     RequestOperationSessionSubmit,
		Stage:         RequestStageSubmitted,
		SessionTarget: "director",
		SessionID:     "director",
		ElapsedMs:     20,
	})
	recordPayloadEvent(t, ep, events.RequestResultSessionSubmit, "director", SessionSubmitSucceededPayload{
		RequestID: "req-done",
		SessionID: "director",
		Queued:    true,
		Intent:    "default",
	})
	h := newTestCityHandler(t, state)

	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/request/req-done"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp RequestStatus
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != requestStatusSucceeded || resp.Stage != RequestStageSubmitted {
		t.Fatalf("response = %#v, want succeeded with submitted stage", resp)
	}
	if resp.Progress == nil || resp.Progress.Type != events.RequestProgress {
		t.Fatalf("progress = %#v, want latest progress event", resp.Progress)
	}
	if resp.Event == nil || resp.Event.Type != events.RequestResultSessionSubmit {
		t.Fatalf("event = %#v, want terminal submit result", resp.Event)
	}
}

func TestRequestStatusAfterSeq(t *testing.T) {
	state := newFakeState(t)
	ep := state.eventProv.(*events.Fake)
	recordPayloadEvent(t, ep, events.RequestResultSessionSubmit, "director", SessionSubmitSucceededPayload{
		RequestID: "req-want",
		SessionID: "director",
	})
	cursor := ep.Events[0].Seq
	h := newTestCityHandler(t, state)

	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/request/req-want?after_seq=1"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp RequestStatus
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if cursor != 1 {
		t.Fatalf("test setup cursor = %d, want 1", cursor)
	}
	if resp.Status != requestStatusPending {
		t.Fatalf("response = %#v, want pending because after_seq excludes existing event", resp)
	}
}

func TestRequestStatusScopesEventScansToRequestTypes(t *testing.T) {
	state := newFakeState(t)
	ep := state.eventProv.(*events.Fake)
	for i := 0; i < 25; i++ {
		recordPayloadEvent(t, ep, events.BeadUpdated, "noise", events.NoPayload{})
	}
	recordPayloadEvent(t, ep, events.RequestProgress, "director", RequestProgressPayload{
		RequestID:     "req-fast",
		Operation:     RequestOperationSessionSubmit,
		Stage:         RequestStageDelivering,
		SessionTarget: "director",
		ElapsedMs:     7,
	})
	recordPayloadEvent(t, ep, events.RequestResultSessionSubmit, "director", SessionSubmitSucceededPayload{
		RequestID: "req-fast",
		SessionID: "director",
	})
	recorder := &recordingEventProvider{Provider: ep}
	state.eventProv = recorder
	h := newTestCityHandler(t, state)

	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/request/req-fast"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp RequestStatus
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != requestStatusSucceeded || resp.Event == nil {
		t.Fatalf("response = %#v, want succeeded with terminal event", resp)
	}
	if len(recorder.filters) == 0 {
		t.Fatal("expected request status to list events")
	}
	allowed := map[string]bool{
		events.RequestProgress:             true,
		events.RequestResultCityCreate:     true,
		events.RequestResultCityUnregister: true,
		events.RequestResultSessionCreate:  true,
		events.RequestResultSessionMessage: true,
		events.RequestResultSessionSubmit:  true,
		events.RequestFailed:               true,
	}
	seenProgress := false
	seenSubmitResult := false
	for _, filter := range recorder.filters {
		if filter.Type == "" && len(filter.Types) == 0 {
			t.Fatalf("request status used broad event scan: %#v", filter)
		}
		filterTypes := filter.Types
		if filter.Type != "" {
			filterTypes = append(filterTypes, filter.Type)
		}
		for _, eventType := range filterTypes {
			if !allowed[eventType] {
				t.Fatalf("request status scanned non-request event type %q", eventType)
			}
			if eventType == events.RequestProgress {
				seenProgress = true
			}
			if eventType == events.RequestResultSessionSubmit {
				seenSubmitResult = true
			}
		}
		if len(filterTypes) == 0 {
			t.Fatalf("request status scanned non-request event type %q", filter.Type)
		}
	}
	if !seenProgress || !seenSubmitResult {
		t.Fatalf("filters = %#v, want progress and session.submit result scans", recorder.filters)
	}
}

func TestRequestStatusRejectsInvalidAfterSeq(t *testing.T) {
	state := newFakeState(t)
	h := newTestCityHandler(t, state)

	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/request/req-1?after_seq=oops"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestRequestStatusRequiresEvents(t *testing.T) {
	state := newFakeState(t)
	state.eventProv = nil
	h := newTestCityHandler(t, state)

	req := httptest.NewRequest(http.MethodGet, cityURL(state, "/request/req-1"), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func recordPayloadEvent(t *testing.T, ep events.Recorder, eventType, subject string, payload events.Payload) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	ep.Record(events.Event{
		Type:    eventType,
		Actor:   "api",
		Subject: subject,
		Payload: raw,
	})
}

type recordingEventProvider struct {
	events.Provider
	filters []events.Filter
}

func (p *recordingEventProvider) List(filter events.Filter) ([]events.Event, error) {
	p.filters = append(p.filters, filter)
	return p.Provider.List(filter)
}
