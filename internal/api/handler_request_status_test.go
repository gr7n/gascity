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
	if resp.Event == nil || resp.Event.Type != events.RequestFailed {
		t.Fatalf("event = %#v, want request.failed", resp.Event)
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
