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

	evts, err := ep.List(events.Filter{AfterSeq: afterSeq})
	if err != nil {
		return result, fmt.Errorf("list events: %w", err)
	}

	var best *RequestStatus
	var bestSeq uint64
	for _, event := range evts {
		candidate, match, err := requestStatusFromEvent(event, requestID)
		if err != nil {
			log.Printf("api: request status skip event type=%s seq=%d: %v", event.Type, event.Seq, err)
			continue
		}
		if !match {
			continue
		}
		if best == nil || event.Seq < bestSeq {
			candidateCopy := candidate
			best = &candidateCopy
			bestSeq = event.Seq
		}
	}

	if best != nil {
		return *best, nil
	}
	return result, nil
}

func requestStatusFromEvent(event events.Event, requestID string) (RequestStatus, bool, error) {
	operation, terminal := asyncRequestOperationByEventType[event.Type]
	if !terminal {
		return RequestStatus{}, false, nil
	}

	var payload struct {
		RequestID string `json:"request_id"`
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return RequestStatus{}, false, fmt.Errorf("decode request payload: %w", err)
	}
	if payload.RequestID != requestID {
		return RequestStatus{}, false, nil
	}
	if operation == "" {
		operation = payload.Operation
	}

	wire, ok := toWireEvent(event)
	if !ok {
		return RequestStatus{}, false, fmt.Errorf("decode terminal event %s", event.Type)
	}

	status := requestStatusSucceeded
	if event.Type == events.RequestFailed {
		status = requestStatusFailed
	}
	return RequestStatus{
		RequestID: requestID,
		Status:    status,
		Operation: operation,
		Event:     &wire,
	}, true, nil
}
