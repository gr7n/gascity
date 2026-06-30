package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/events"
)

const (
	requestStatusPending   = "pending"
	requestStatusSucceeded = "succeeded"
	requestStatusFailed    = "failed"

	// Cursorless status checks are a recovery path for recent async requests,
	// not a durable event-history query. Keep them bounded so stale/missing
	// request IDs cannot make UI health checks scan large city logs.
	requestStatusTailScanLimit = 64
)

var asyncRequestOperationByEventType = map[string]string{
	events.RequestResultCityCreate:     RequestOperationCityCreate,
	events.RequestResultCityUnregister: RequestOperationCityUnregister,
	events.RequestResultSessionCreate:  RequestOperationSessionCreate,
	events.RequestResultSessionMessage: RequestOperationSessionMessage,
	events.RequestResultSessionSubmit:  RequestOperationSessionSubmit,
	events.RequestFailed:               "",
}

// humaHandleRequestStatus is the Huma-typed handler for
// GET /v0/city/{cityName}/request/{id}. It gives polling clients a durable
// fallback for async operation results when an SSE frame is missed.
func (s *Server) humaHandleRequestStatus(_ context.Context, input *RequestStatusInput) (*IndexOutput[RequestStatus], error) {
	requestID := strings.TrimSpace(input.ID)
	if requestID == "" {
		return nil, huma.Error400BadRequest("request id is required")
	}

	afterSeq, err := parseOptionalAfterSeq(input.AfterSeq)
	if err != nil {
		return nil, err
	}

	ep := s.state.EventProvider()
	if ep == nil {
		return nil, huma.Error503ServiceUnavailable("events not enabled")
	}

	status, err := lookupAsyncRequestStatus(ep, requestID, afterSeq)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}

	return &IndexOutput[RequestStatus]{
		Index: s.latestIndex(),
		Body:  status,
	}, nil
}

// humaHandleSupervisorRequestStatus is the supervisor-scope counterpart to
// GET /v0/city/{cityName}/request/{id}. It lets clients poll for city
// lifecycle request results without depending solely on the global SSE stream.
func (sm *SupervisorMux) humaHandleSupervisorRequestStatus(_ context.Context, input *SupervisorRequestStatusInput) (*SupervisorRequestStatusOutput, error) {
	requestID := strings.TrimSpace(input.ID)
	if requestID == "" {
		return nil, huma.Error400BadRequest("request id is required")
	}

	status, err := lookupSupervisorAsyncRequestStatus(sm.buildMultiplexer(), requestID, input.AfterCursor)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}

	return &SupervisorRequestStatusOutput{Body: status}, nil
}

func parseOptionalAfterSeq(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	afterSeq, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, huma.Error400BadRequest("invalid after_seq: " + err.Error())
	}
	return afterSeq, nil
}

func lookupAsyncRequestStatus(ep events.Provider, requestID string, afterSeq uint64) (RequestStatus, error) {
	result := RequestStatus{
		RequestID: requestID,
		Status:    requestStatusPending,
	}

	evts, err := listAsyncRequestStatusEvents(ep, requestID, afterSeq)
	if err != nil {
		return result, fmt.Errorf("list events: %w", err)
	}

	var terminal *RequestStatus
	var terminalSeq uint64
	var progress *RequestStatus
	var progressSeq uint64
	for _, event := range evts {
		candidate, match, err := requestStatusFromEvent(event, requestID)
		if err != nil {
			log.Printf("api: request status skip event type=%s seq=%d: %v", event.Type, event.Seq, err)
			continue
		}
		if !match {
			continue
		}
		switch candidate.Status {
		case requestStatusPending:
			if progress == nil || event.Seq > progressSeq {
				candidateCopy := candidate
				progress = &candidateCopy
				progressSeq = event.Seq
			}
		default:
			if terminal == nil || event.Seq < terminalSeq {
				candidateCopy := candidate
				terminal = &candidateCopy
				terminalSeq = event.Seq
			}
		}
	}

	if terminal != nil {
		if progress != nil {
			if terminal.Stage == "" {
				terminal.Stage = progress.Stage
			}
			if terminal.Progress == nil {
				terminal.Progress = progress.Progress
			}
		}
		return *terminal, nil
	}
	if progress != nil {
		return *progress, nil
	}
	return result, nil
}

func listAsyncRequestStatusEvents(ep events.Provider, requestID string, afterSeq uint64) ([]events.Event, error) {
	if requestLookup, ok := ep.(events.RequestEventProvider); ok {
		evts, err := requestLookup.ListRequestEvents(requestID, afterSeq)
		if err == nil {
			return evts, nil
		}
	}
	filter := events.Filter{AfterSeq: afterSeq, Types: events.RequestEventTypes}
	if afterSeq == 0 {
		if tail, ok := ep.(events.TailProvider); ok {
			return tail.ListTail(filter, requestStatusTailScanLimit)
		}
	}
	return ep.List(filter)
}

func requestStatusFromEvent(event events.Event, requestID string) (RequestStatus, bool, error) {
	if event.Type == events.RequestProgress {
		return requestProgressStatusFromEvent(event, requestID)
	}

	terminal, match, err := requestTerminalStatusFromEvent(event, requestID)
	if err != nil || !match {
		return RequestStatus{}, match, err
	}

	wire, ok := toWireEvent(event)
	if !ok {
		return RequestStatus{}, false, fmt.Errorf("decode terminal event %s", event.Type)
	}

	return RequestStatus{
		RequestID: requestID,
		Status:    terminal.status,
		Operation: terminal.operation,
		Stage:     terminal.stage,
		Event:     &wire,
	}, true, nil
}

func requestProgressStatusFromEvent(event events.Event, requestID string) (RequestStatus, bool, error) {
	var payload RequestProgressPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return RequestStatus{}, false, fmt.Errorf("decode request progress payload: %w", err)
	}
	if payload.RequestID != requestID {
		return RequestStatus{}, false, nil
	}

	wire, ok := toWireEvent(event)
	if !ok {
		return RequestStatus{}, false, fmt.Errorf("decode progress event %s", event.Type)
	}

	return RequestStatus{
		RequestID: requestID,
		Status:    requestStatusPending,
		Operation: payload.Operation,
		Stage:     payload.Stage,
		Progress:  &wire,
	}, true, nil
}

func lookupSupervisorAsyncRequestStatus(mux *events.Multiplexer, requestID, afterCursor string) (SupervisorRequestStatus, error) {
	result := SupervisorRequestStatus{
		RequestID: requestID,
		Status:    requestStatusPending,
	}

	cursors := events.ParseCursor(strings.TrimSpace(afterCursor))
	evts, err := listSupervisorAsyncRequestStatusEvents(mux, requestID, cursors)
	if err != nil {
		return result, fmt.Errorf("list supervisor events: %w", err)
	}

	var terminal *SupervisorRequestStatus
	var terminalSeq uint64
	var progress *SupervisorRequestStatus
	var progressSeq uint64
	for _, event := range evts {
		candidate, match, err := supervisorRequestStatusFromEvent(event, requestID)
		if err != nil {
			log.Printf("api: supervisor request status skip event city=%s type=%s seq=%d: %v", event.City, event.Type, event.Seq, err)
			continue
		}
		if !match {
			continue
		}
		switch candidate.Status {
		case requestStatusPending:
			if progress == nil || event.Seq > progressSeq {
				candidateCopy := candidate
				progress = &candidateCopy
				progressSeq = event.Seq
			}
		default:
			if terminal == nil || event.Seq < terminalSeq {
				candidateCopy := candidate
				terminal = &candidateCopy
				terminalSeq = event.Seq
			}
		}
	}

	if terminal != nil {
		if progress != nil {
			if terminal.Stage == "" {
				terminal.Stage = progress.Stage
			}
			if terminal.Progress == nil {
				terminal.Progress = progress.Progress
			}
		}
		return *terminal, nil
	}
	if progress != nil {
		return *progress, nil
	}

	return result, nil
}

func listSupervisorAsyncRequestStatusEvents(mux *events.Multiplexer, requestID string, cursors map[string]uint64) ([]events.TaggedEvent, error) {
	filter := events.Filter{Types: events.RequestEventTypes}
	if len(cursors) == 0 {
		return mux.ListTail(filter, requestStatusTailScanLimit)
	}
	return mux.ListRequestEventsAfterCursor(requestID, cursors, filter)
}

func supervisorRequestStatusFromEvent(event events.TaggedEvent, requestID string) (SupervisorRequestStatus, bool, error) {
	if event.Type == events.RequestProgress {
		return supervisorRequestProgressStatusFromEvent(event, requestID)
	}

	terminal, match, err := requestTerminalStatusFromEvent(event.Event, requestID)
	if err != nil || !match {
		return SupervisorRequestStatus{}, match, err
	}

	wire, ok := toWireTaggedEvent(event)
	if !ok {
		return SupervisorRequestStatus{}, false, fmt.Errorf("decode terminal tagged event %s", event.Type)
	}

	return SupervisorRequestStatus{
		RequestID: requestID,
		Status:    terminal.status,
		Operation: terminal.operation,
		Stage:     terminal.stage,
		Event:     &wire,
	}, true, nil
}

func supervisorRequestProgressStatusFromEvent(event events.TaggedEvent, requestID string) (SupervisorRequestStatus, bool, error) {
	var payload RequestProgressPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return SupervisorRequestStatus{}, false, fmt.Errorf("decode request progress payload: %w", err)
	}
	if payload.RequestID != requestID {
		return SupervisorRequestStatus{}, false, nil
	}

	wire, ok := toWireTaggedEvent(event)
	if !ok {
		return SupervisorRequestStatus{}, false, fmt.Errorf("decode progress tagged event %s", event.Type)
	}

	return SupervisorRequestStatus{
		RequestID: requestID,
		Status:    requestStatusPending,
		Operation: payload.Operation,
		Stage:     payload.Stage,
		Progress:  &wire,
	}, true, nil
}

type terminalRequestStatus struct {
	status    string
	operation string
	stage     string
}

func requestTerminalStatusFromEvent(event events.Event, requestID string) (terminalRequestStatus, bool, error) {
	operation, terminal := asyncRequestOperationByEventType[event.Type]
	if !terminal {
		return terminalRequestStatus{}, false, nil
	}

	var payload struct {
		RequestID string `json:"request_id"`
		Operation string `json:"operation"`
		Stage     string `json:"stage"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return terminalRequestStatus{}, false, fmt.Errorf("decode request payload: %w", err)
	}
	if payload.RequestID != requestID {
		return terminalRequestStatus{}, false, nil
	}
	if operation == "" {
		operation = payload.Operation
	}

	status := requestStatusSucceeded
	if event.Type == events.RequestFailed {
		status = requestStatusFailed
	}
	return terminalRequestStatus{
		status:    status,
		operation: operation,
		stage:     payload.Stage,
	}, true, nil
}
