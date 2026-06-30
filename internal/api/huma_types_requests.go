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
	Event     *WireEvent `json:"event,omitempty" doc:"Terminal result event when the request has succeeded or failed."`
}
