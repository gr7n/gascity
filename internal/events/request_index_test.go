package events

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRequestIDFromEventExtractsRequestEventsOnly(t *testing.T) {
	requestEvent := Event{
		Type:    RequestResultSessionSubmit,
		Payload: json.RawMessage(`{"request_id":" req-1 "}`),
	}
	if got, ok := RequestIDFromEvent(requestEvent); !ok || got != "req-1" {
		t.Fatalf("RequestIDFromEvent(request) = %q, %v; want req-1, true", got, ok)
	}

	noise := Event{
		Type:    BeadUpdated,
		Payload: json.RawMessage(`{"request_id":"req-1"}`),
	}
	if got, ok := RequestIDFromEvent(noise); ok || got != "" {
		t.Fatalf("RequestIDFromEvent(noise) = %q, %v; want empty, false", got, ok)
	}
}

func TestFileRecorderListRequestEventsBuildsLazySidecar(t *testing.T) {
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "events.jsonl")
	rec, err := NewFileRecorder(path, &stderr)
	if err != nil {
		t.Fatalf("NewFileRecorder: %v", err)
	}
	defer rec.Close() //nolint:errcheck // test cleanup

	rec.Record(Event{Type: BeadUpdated, Actor: "noise", Payload: json.RawMessage(`{"request_id":"req-want"}`)})
	rec.Record(requestIndexTestEvent(RequestProgress, "req-other"))
	rec.Record(requestIndexTestEvent(RequestResultSessionSubmit, "req-want"))

	got, err := rec.ListRequestEvents("req-want", 0)
	if err != nil {
		t.Fatalf("ListRequestEvents: %v", err)
	}
	if len(got) != 1 || got[0].Type != RequestResultSessionSubmit {
		t.Fatalf("ListRequestEvents() = %+v, want only req-want submit result", got)
	}

	sidecar := requestIndexPath(path)
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("request index sidecar not created: %v", err)
	}

	rec.Record(requestIndexTestEvent(RequestProgress, "req-want"))
	catchup, err := rec.ListRequestEvents("req-want", got[0].Seq)
	if err != nil {
		t.Fatalf("ListRequestEvents catchup: %v", err)
	}
	if len(catchup) != 1 || catchup[0].Type != RequestProgress {
		t.Fatalf("catchup = %+v, want one new progress event", catchup)
	}
}

func TestFileRecorderRequestIndexSkipsMalformedRowsAndDedupesCrashReplay(t *testing.T) {
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "events.jsonl")
	rec, err := NewFileRecorder(path, &stderr)
	if err != nil {
		t.Fatalf("NewFileRecorder: %v", err)
	}
	defer rec.Close() //nolint:errcheck // test cleanup

	rec.Record(requestIndexTestEvent(RequestResultSessionSubmit, "req-want"))
	sidecar := requestIndexPath(path)
	row := requestIndexRow{
		Version:   requestIndexVersion,
		Kind:      requestIndexKindEvent,
		RequestID: "req-want",
		Event:     rec.EventsForTest(t)[0],
	}
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal sidecar row: %v", err)
	}
	if err := os.WriteFile(sidecar, append(append([]byte("not json\n"), data...), '\n'), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	got, err := rec.ListRequestEvents("req-want", 0)
	if err != nil {
		t.Fatalf("ListRequestEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListRequestEvents() = %+v, want one deduped event", got)
	}
}

func TestFileRecorderListRequestEventsAfterFreshCursorDoesNotBuildPartialSidecar(t *testing.T) {
	var stderr bytes.Buffer
	path := filepath.Join(t.TempDir(), "events.jsonl")
	rec, err := NewFileRecorder(path, &stderr)
	if err != nil {
		t.Fatalf("NewFileRecorder: %v", err)
	}
	defer rec.Close() //nolint:errcheck // test cleanup

	for i := 0; i < 12; i++ {
		rec.Record(requestIndexTestEvent(RequestProgress, "req-old"))
	}
	cursor := rec.EventsForTest(t)[11].Seq
	rec.Record(requestIndexTestEvent(RequestProgress, "req-other"))
	rec.Record(requestIndexTestEvent(RequestResultSessionSubmit, "req-want"))

	got, err := rec.ListRequestEvents("req-want", cursor)
	if err != nil {
		t.Fatalf("ListRequestEvents after cursor: %v", err)
	}
	if len(got) != 1 || got[0].Type != RequestResultSessionSubmit {
		t.Fatalf("ListRequestEvents after cursor = %+v, want one submit result", got)
	}
	if _, err := os.Stat(requestIndexPath(path)); !os.IsNotExist(err) {
		t.Fatalf("fresh cursor lookup created request index sidecar: %v", err)
	}

	historical, err := rec.ListRequestEvents("req-old", 0)
	if err != nil {
		t.Fatalf("historical ListRequestEvents: %v", err)
	}
	if len(historical) != 12 {
		t.Fatalf("historical events = %d, want 12", len(historical))
	}
	if _, err := os.Stat(requestIndexPath(path)); err != nil {
		t.Fatalf("historical lookup did not create request index sidecar: %v", err)
	}
}

func (r *FileRecorder) EventsForTest(t *testing.T) []Event {
	t.Helper()
	events, err := r.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return events
}

func requestIndexTestEvent(eventType, requestID string) Event {
	return Event{
		Type:    eventType,
		Actor:   "api",
		Payload: json.RawMessage(`{"request_id":"` + requestID + `"}`),
	}
}
