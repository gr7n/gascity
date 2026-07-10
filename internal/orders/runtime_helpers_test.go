package orders

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

type rowsErrorStore struct {
	*beads.MemStore
	rows []beads.Bead
	err  error
}

func (s *rowsErrorStore) List(_ beads.ListQuery) ([]beads.Bead, error) {
	return s.rows, s.err
}

func TestLastRunFuncForStoreReturnsLatestRun(t *testing.T) {
	store := beads.NewMemStore()

	first, err := store.Create(beads.Bead{
		Title:  "order:digest",
		Status: "closed",
		Labels: []string{"order-run:digest"},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(time.Millisecond)

	second, err := store.Create(beads.Bead{
		Title:  "order:digest",
		Status: "closed",
		Labels: []string{"order-run:digest", "wisp-failed"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := LastRunFuncForStore(store)("digest")
	if err != nil {
		t.Fatalf("LastRunFuncForStore(): %v", err)
	}
	if !got.Equal(second.CreatedAt) {
		t.Fatalf("LastRunFuncForStore() = %s, want %s (latest run should remain authoritative)", got, second.CreatedAt)
	}
	if !second.CreatedAt.After(first.CreatedAt) {
		t.Fatalf("test setup invalid: second.CreatedAt=%s, first.CreatedAt=%s", second.CreatedAt, first.CreatedAt)
	}
}

func TestLastRunFuncForStoreReturnsZeroWhenNoRunsExist(t *testing.T) {
	store := beads.NewMemStore()

	got, err := LastRunFuncForStore(store)("digest")
	if err != nil {
		t.Fatalf("LastRunFuncForStore(): %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("LastRunFuncForStore() = %s, want zero time", got)
	}
}

func TestLastRunFuncForStoreUsesRowsFromPartialTierError(t *testing.T) {
	want := time.Date(2026, 5, 15, 7, 0, 0, 0, time.UTC)
	store := &rowsErrorStore{
		MemStore: beads.NewMemStore(),
		rows: []beads.Bead{{
			ID:        "run-1",
			Title:     "digest",
			CreatedAt: want,
			Labels:    []string{"order-run:digest"},
		}},
		err: errors.New("wisps tier unavailable"),
	}

	got, err := LastRunFuncForStore(store)("digest")
	if err != nil {
		t.Fatalf("LastRunFuncForStore(): %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("LastRunFuncForStore() = %s, want %s from surviving rows", got, want)
	}
}

func TestCursorFuncForStoreUsesRowsAndLogsPartialTierError(t *testing.T) {
	oldLogf := runtimeHelpersLogf
	var logs []string
	runtimeHelpersLogf = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() {
		runtimeHelpersLogf = oldLogf
	})
	store := &rowsErrorStore{
		MemStore: beads.NewMemStore(),
		rows: []beads.Bead{{
			ID:     "run-1",
			Labels: []string{"order-run:digest", "seq:42"},
		}},
		err: errors.New("wisps tier unavailable"),
	}

	got := CursorFuncForStore(store)("digest")
	if got != 42 {
		t.Fatalf("CursorFuncForStore() = %d, want 42 from surviving rows", got)
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "partially failed") {
		t.Fatalf("logs = %#v, want partial failure log", logs)
	}
}

type queryRecordingStore struct {
	beads.Store
	queries []beads.ListQuery
}

func (s *queryRecordingStore) List(q beads.ListQuery) ([]beads.Bead, error) {
	s.queries = append(s.queries, q)
	return s.Store.List(q)
}

func (s *queryRecordingStore) listCounts() (window, perName int) {
	for _, q := range s.queries {
		switch {
		case q.Label == orderTrackingLabel:
			window++
		case strings.HasPrefix(q.Label, "order-run:"):
			perName++
		}
	}
	return window, perName
}

type indexedQueryRecordingStore struct {
	*queryRecordingStore
	lastRun       map[string]time.Time
	calls         []string
	snapshotCalls int
}

func (s *indexedQueryRecordingStore) LastOrderRun(name string) (time.Time, error) {
	s.calls = append(s.calls, name)
	return s.lastRun[name], nil
}

func (s *indexedQueryRecordingStore) LastOrderRuns() (map[string]time.Time, error) {
	s.snapshotCalls++
	out := make(map[string]time.Time, len(s.lastRun))
	for name, last := range s.lastRun {
		out[name] = last
	}
	return out, nil
}

type nonComparableIndexedStore struct {
	beads.Store
	labels        []string
	lastRun       map[string]time.Time
	snapshotCalls *int
}

func (s nonComparableIndexedStore) LastOrderRuns() (map[string]time.Time, error) {
	(*s.snapshotCalls)++
	out := make(map[string]time.Time, len(s.lastRun))
	for name, last := range s.lastRun {
		out[name] = last
	}
	return out, nil
}

func TestLastRunBatchUsesAuthoritativeStoreIndex(t *testing.T) {
	mem := beads.NewMemStore()
	created := make(map[string]time.Time, 3)
	for _, name := range []string{"digest", "sweep", "lint"} {
		bead, err := mem.Create(beads.Bead{
			Title:  "order:" + name,
			Status: "closed",
			Labels: []string{orderTrackingLabel, "order-run:" + name},
		})
		if err != nil {
			t.Fatal(err)
		}
		created[name] = bead.CreatedAt
		time.Sleep(time.Millisecond)
	}

	store := &indexedQueryRecordingStore{
		queryRecordingStore: &queryRecordingStore{Store: mem},
		lastRun:             created,
	}
	fn := NewLastRunBatch(100).AcrossStores(store)
	for name, want := range created {
		got, err := fn(name)
		if err != nil {
			t.Fatalf("batched last run %s: %v", name, err)
		}
		if !got.Equal(want) {
			t.Fatalf("batched last run %s = %s, want %s", name, got, want)
		}
	}

	window, perName := store.listCounts()
	if window != 0 || perName != 0 {
		t.Fatalf("store lists = window %d / per-name %d, want 0 / 0 (indexed store serves lookups)", window, perName)
	}
	if store.snapshotCalls != 1 {
		t.Fatalf("LastOrderRuns calls = %d, want 1", store.snapshotCalls)
	}
	if len(store.calls) != 0 {
		t.Fatalf("LastOrderRun calls = %d, want 0", len(store.calls))
	}
}

func TestLastRunBatchReusesAuthoritativeSnapshotAcrossResolvers(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	store := &indexedQueryRecordingStore{
		queryRecordingStore: &queryRecordingStore{Store: beads.NewMemStore()},
		lastRun: map[string]time.Time{
			"digest": now,
			"sweep":  now.Add(time.Minute),
			"lint":   now.Add(2 * time.Minute),
		},
	}

	batch := NewLastRunBatch(100)
	for name, want := range store.lastRun {
		got, err := batch.AcrossStores(store)(name)
		if err != nil {
			t.Fatalf("batched last run %s: %v", name, err)
		}
		if !got.Equal(want) {
			t.Fatalf("batched last run %s = %s, want %s", name, got, want)
		}
	}

	if store.snapshotCalls != 1 {
		t.Fatalf("LastOrderRuns calls = %d, want 1 across repeated AcrossStores calls", store.snapshotCalls)
	}
}

func TestLastRunBatchDoesNotPanicForNonComparableIndexedStore(t *testing.T) {
	now := time.Date(2026, 6, 15, 11, 0, 0, 0, time.UTC)
	snapshotCalls := 0
	store := nonComparableIndexedStore{
		Store:         beads.NewMemStore(),
		labels:        []string{"non-comparable"},
		snapshotCalls: &snapshotCalls,
		lastRun: map[string]time.Time{
			"digest": now,
			"sweep":  now.Add(time.Minute),
		},
	}

	batch := NewLastRunBatch(100)
	for name, want := range store.lastRun {
		got, err := batch.AcrossStores(store)(name)
		if err != nil {
			t.Fatalf("batched last run %s: %v", name, err)
		}
		if !got.Equal(want) {
			t.Fatalf("batched last run %s = %s, want %s", name, got, want)
		}
	}

	if snapshotCalls == 0 {
		t.Fatal("LastOrderRuns was not called")
	}
}

func TestLastOrderRunsForStoreScansExactOrderRunHistory(t *testing.T) {
	mem := beads.NewMemStore()
	tracked, err := mem.Create(beads.Bead{
		Title:  "order:digest",
		Status: "closed",
		Labels: []string{orderTrackingLabel, "order-run:digest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	manual, err := mem.Create(beads.Bead{
		Title:  "manual digest",
		Status: "closed",
		Labels: []string{"order-run:digest"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := LastOrderRunsForStore(mem)
	if err != nil {
		t.Fatalf("LastOrderRunsForStore: %v", err)
	}
	if got["digest"].Equal(tracked.CreatedAt) {
		t.Fatalf("snapshot used tracking row %s, want newer manual row %s", tracked.CreatedAt, manual.CreatedAt)
	}
	if !got["digest"].Equal(manual.CreatedAt) {
		t.Fatalf("snapshot digest = %s, want %s", got["digest"], manual.CreatedAt)
	}
}

func TestLastRunBatchUsesExactLookupWithoutStoreIndex(t *testing.T) {
	mem := beads.NewMemStore()
	run, err := mem.Create(beads.Bead{
		Title:  "order:digest",
		Status: "closed",
		Labels: []string{"order-run:digest"},
	})
	if err != nil {
		t.Fatal(err)
	}

	store := &queryRecordingStore{Store: mem}
	got, err := NewLastRunBatch(100).AcrossStores(store)("digest")
	if err != nil {
		t.Fatalf("batched last run: %v", err)
	}
	if !got.Equal(run.CreatedAt) {
		t.Fatalf("batched last run = %s, want %s from exact lookup", got, run.CreatedAt)
	}
	window, perName := store.listCounts()
	if window != 0 || perName != 1 {
		t.Fatalf("lists = window %d / per-name %d, want 0 / 1 (no tracking-window shortcut)", window, perName)
	}
}

func TestLastRunBatchReturnsNewestManualRunAfterTrackingHit(t *testing.T) {
	mem := beads.NewMemStore()
	tracked, err := mem.Create(beads.Bead{
		Title:  "order:digest",
		Status: "closed",
		Labels: []string{orderTrackingLabel, "order-run:digest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	manual, err := mem.Create(beads.Bead{
		Title:  "order:digest",
		Status: "closed",
		Labels: []string{"order-run:digest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !manual.CreatedAt.After(tracked.CreatedAt) {
		t.Fatalf("test setup invalid: manual.CreatedAt=%s, tracked.CreatedAt=%s", manual.CreatedAt, tracked.CreatedAt)
	}

	store := &queryRecordingStore{Store: mem}
	got, err := NewLastRunBatch(100).AcrossStores(store)("digest")
	if err != nil {
		t.Fatalf("batched last run: %v", err)
	}
	if !got.Equal(manual.CreatedAt) {
		t.Fatalf("batched last run = %s, want newer manual run %s", got, manual.CreatedAt)
	}
	window, perName := store.listCounts()
	if window != 0 || perName != 1 {
		t.Fatalf("lists = window %d / per-name %d, want 0 / 1 (tracking hits must not mask exact order-run history)", window, perName)
	}
}

func TestLastRunBatchMergesNewestAcrossStores(t *testing.T) {
	older := beads.NewMemStore()
	if _, err := older.Create(beads.Bead{
		Title:  "order:digest",
		Status: "closed",
		Labels: []string{orderTrackingLabel, "order-run:digest"},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	newer := beads.NewMemStore()
	want, err := newer.Create(beads.Bead{
		Title:  "order:digest",
		Status: "closed",
		Labels: []string{orderTrackingLabel, "order-run:digest"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := NewLastRunBatch(100).AcrossStores(older, newer)("digest")
	if err != nil {
		t.Fatalf("batched last run: %v", err)
	}
	if !got.Equal(want.CreatedAt) {
		t.Fatalf("batched last run = %s, want newest across stores %s", got, want.CreatedAt)
	}
}
