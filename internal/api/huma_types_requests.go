package api

// RequestStatusInput is the Huma input for GET
// /v0/city/{cityName}/request/{id}.
type RequestStatusInput struct {
	CityScope
	ID       string `path:"id" doc:"Async request ID returned by a 202 response."`
	AfterSeq string `query:"after_seq" required:"false" doc:"Only inspect city events after this sequence. Pass the event_cursor from the 202 response for efficient polling."`
}

// RequestStatus is the durable read-side status for an async request.
type RequestStatus struct {
	RequestID string     `json:"request_id" doc:"Async request ID."`
	Status    string     `json:"status" enum:"pending,succeeded,failed" doc:"Current request state derived from terminal async-result events."`
	Operation string     `json:"operation,omitempty" enum:"city.create,city.unregister,session.create,session.message,session.submit" doc:"Async operation once known."`
	Stage     string     `json:"stage,omitempty" enum:"resolving,materializing,delivering,submitted,timeout" doc:"Latest async request progress stage, if known."`
	Progress  *WireEvent `json:"progress,omitempty" doc:"Latest non-terminal progress event for this request, if one was observed."`
	Event     *WireEvent `json:"event,omitempty" doc:"Terminal result event when the request has succeeded or failed."`
}

// SupervisorRequestStatusInput is the Huma input for GET
// /v0/request/{id}.
type SupervisorRequestStatusInput struct {
	ID          string `path:"id" doc:"Async request ID returned by a 202 response."`
	AfterCursor string `query:"after_cursor" required:"false" doc:"Only inspect supervisor/global events after this composite cursor. Pass the event_cursor from the 202 response for efficient polling."`
}

// SupervisorRequestStatus is the supervisor/global read-side status for an
// async request. The terminal event is tagged with the city/source that
// produced it.
type SupervisorRequestStatus struct {
	RequestID string           `json:"request_id" doc:"Async request ID."`
	Status    string           `json:"status" enum:"pending,succeeded,failed" doc:"Current request state derived from terminal async-result events."`
	Operation string           `json:"operation,omitempty" enum:"city.create,city.unregister,session.create,session.message,session.submit" doc:"Async operation once known."`
	Stage     string           `json:"stage,omitempty" enum:"resolving,materializing,delivering,submitted,timeout" doc:"Latest async request progress stage, if known."`
	Progress  *WireTaggedEvent `json:"progress,omitempty" doc:"Latest non-terminal tagged progress event for this request, if one was observed."`
	Event     *WireTaggedEvent `json:"event,omitempty" doc:"Terminal tagged result event when the request has succeeded or failed."`
}

// SupervisorRequestStatusOutput is the response for GET /v0/request/{id}.
type SupervisorRequestStatusOutput struct {
	Body SupervisorRequestStatus
}
