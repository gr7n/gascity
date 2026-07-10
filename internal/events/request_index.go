package events

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	requestIndexVersion        = 1
	requestIndexKindEvent      = "event"
	requestIndexKindCheckpoint = "checkpoint"
)

type requestIndexRow struct {
	Version           int    `json:"v"`
	Kind              string `json:"kind"`
	RequestID         string `json:"request_id,omitempty"`
	IndexedThroughSeq uint64 `json:"indexed_through_seq,omitempty"`
	Event             Event  `json:"event,omitempty"`
}

// RequestIDFromEvent extracts the async request correlation ID from request
// progress/result events. It returns false for non-request events, malformed
// payloads, or request events that do not carry request_id.
func RequestIDFromEvent(event Event) (string, bool) {
	if !isRequestEventType(event.Type) {
		return "", false
	}
	var payload struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return "", false
	}
	requestID := strings.TrimSpace(payload.RequestID)
	return requestID, requestID != ""
}

// ListRequestEvents returns async request events for requestID with Seq greater
// than afterSeq, using a lazy sidecar index backed by the canonical event log.
func (r *FileRecorder) ListRequestEvents(requestID string, afterSeq uint64) ([]Event, error) {
	return lookupRequestEvents(r.path, requestID, afterSeq)
}

func lookupRequestEvents(eventPath, requestID string, afterSeq uint64) ([]Event, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, nil
	}
	indexPath := requestIndexPath(eventPath)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating request index dir: %w", err)
	}

	lockFile, err := lockRequestIndex(indexPath)
	if err != nil {
		return nil, err
	}
	defer lockFile.Close()                                   //nolint:errcheck // closing releases the flock.
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) //nolint:errcheck // best-effort unlock.

	matches, indexedThrough, err := readRequestIndex(indexPath, requestID, afterSeq)
	if err != nil {
		return nil, err
	}
	latestSeq, err := ReadLatestSeq(eventPath)
	if err != nil {
		return nil, err
	}
	if indexedThrough > latestSeq {
		if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("reset stale request index: %w", err)
		}
		matches = nil
		indexedThrough = 0
	}
	if afterSeq > indexedThrough {
		return listRequestEventsAfterSeq(eventPath, requestID, afterSeq)
	}
	if indexedThrough < latestSeq {
		caughtUp, err := appendRequestIndexCatchup(indexPath, eventPath, indexedThrough, latestSeq, requestID, afterSeq)
		if err != nil {
			return nil, err
		}
		matches = append(matches, caughtUp...)
	}
	return dedupeRequestEvents(matches), nil
}

func listRequestEventsAfterSeq(eventPath, requestID string, afterSeq uint64) ([]Event, error) {
	evts, err := ReadFilteredAfterSeq(eventPath, Filter{AfterSeq: afterSeq, Types: RequestEventTypes})
	if err != nil {
		return nil, fmt.Errorf("reading request events after seq: %w", err)
	}
	matches := make([]Event, 0, len(evts))
	for _, event := range evts {
		eventRequestID, ok := RequestIDFromEvent(event)
		if !ok || eventRequestID != requestID {
			continue
		}
		matches = append(matches, event)
	}
	return dedupeRequestEvents(matches), nil
}

func readRequestIndex(indexPath, requestID string, afterSeq uint64) ([]Event, uint64, error) {
	f, err := os.Open(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("reading request index: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only file.

	var matches []Event
	var indexedThrough uint64
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row requestIndexRow
		if err := json.Unmarshal(line, &row); err != nil {
			continue
		}
		if row.Version != requestIndexVersion {
			continue
		}
		switch row.Kind {
		case requestIndexKindCheckpoint:
			if row.IndexedThroughSeq > indexedThrough {
				indexedThrough = row.IndexedThroughSeq
			}
		case requestIndexKindEvent:
			if row.RequestID == requestID && row.Event.Seq > afterSeq {
				matches = append(matches, row.Event)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return matches, indexedThrough, fmt.Errorf("scanning request index: %w", err)
	}
	return matches, indexedThrough, nil
}

func appendRequestIndexCatchup(indexPath, eventPath string, indexedThrough, latestSeq uint64, requestID string, afterSeq uint64) ([]Event, error) {
	events, err := ReadFiltered(eventPath, Filter{AfterSeq: indexedThrough, Types: RequestEventTypes})
	if err != nil {
		return nil, fmt.Errorf("catching up request index: %w", err)
	}
	f, err := os.OpenFile(indexPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening request index: %w", err)
	}
	defer f.Close() //nolint:errcheck // append-only sidecar.

	encoder := json.NewEncoder(f)
	var matches []Event
	for _, event := range events {
		if event.Seq > latestSeq {
			continue
		}
		eventRequestID, ok := RequestIDFromEvent(event)
		if !ok {
			continue
		}
		row := requestIndexRow{
			Version:   requestIndexVersion,
			Kind:      requestIndexKindEvent,
			RequestID: eventRequestID,
			Event:     event,
		}
		if err := encoder.Encode(row); err != nil {
			return matches, fmt.Errorf("writing request index event: %w", err)
		}
		if eventRequestID == requestID && event.Seq > afterSeq {
			matches = append(matches, event)
		}
	}
	if err := encoder.Encode(requestIndexRow{
		Version:           requestIndexVersion,
		Kind:              requestIndexKindCheckpoint,
		IndexedThroughSeq: latestSeq,
	}); err != nil {
		return matches, fmt.Errorf("writing request index checkpoint: %w", err)
	}
	if err := f.Sync(); err != nil {
		return matches, fmt.Errorf("syncing request index: %w", err)
	}
	return matches, nil
}

func requestIndexPath(eventPath string) string {
	if strings.HasSuffix(eventPath, ".jsonl") {
		return strings.TrimSuffix(eventPath, ".jsonl") + ".requests.jsonl"
	}
	return eventPath + ".requests.jsonl"
}

func lockRequestIndex(indexPath string) (*os.File, error) {
	lockPath := indexPath + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening request index lock: %w", err)
	}
	deadline := time.Now().Add(recordFlockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close() //nolint:errcheck // best-effort cleanup.
			return nil, fmt.Errorf("locking request index: %w", err)
		}
		if time.Now().After(deadline) {
			f.Close() //nolint:errcheck // best-effort cleanup.
			return nil, fmt.Errorf("locking request index: timed out after %dms waiting on %s", recordFlockTimeout.Milliseconds(), lockPath)
		}
		time.Sleep(recordFlockRetryInterval)
	}
}

func isRequestEventType(eventType string) bool {
	for _, candidate := range RequestEventTypes {
		if eventType == candidate {
			return true
		}
	}
	return false
}

func dedupeRequestEvents(events []Event) []Event {
	seen := make(map[string]struct{}, len(events))
	deduped := make([]Event, 0, len(events))
	for _, event := range events {
		key := fmt.Sprintf("%d\x00%s", event.Seq, event.Type)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, event)
	}
	return deduped
}
