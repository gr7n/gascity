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

type tierRowsStore struct {
	*beads.MemStore
	rows map[beads.TierMode][]beads.Bead
}

func (s *tierRowsStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	rows := append([]beads.Bead(nil), s.rows[query.TierMode]...)
	if query.Limit > 0 && len(rows) > query.Limit {
		rows = rows[:query.Limit]
	}
	return rows, nil
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

func TestLastRunFuncForStoreReadsNewestAcrossLimitedTiers(t *testing.T) {
	oldRun := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	newRun := time.Date(2026, 6, 4, 14, 0, 0, 0, time.UTC)
	store := &tierRowsStore{
		MemStore: beads.NewMemStore(),
		rows: map[beads.TierMode][]beads.Bead{
			beads.TierBoth: {{
				ID:        "durable-old",
				CreatedAt: oldRun,
				Labels:    []string{"order-run:digest"},
			}},
			beads.TierIssues: {{
				ID:        "durable-old",
				CreatedAt: oldRun,
				Labels:    []string{"order-run:digest"},
			}},
			beads.TierWisps: {{
				ID:        "wisp-new",
				CreatedAt: newRun,
				Labels:    []string{"order-run:digest"},
				Ephemeral: true,
			}},
		},
	}

	got, err := LastRunFuncForStore(store)("digest")
	if err != nil {
		t.Fatalf("LastRunFuncForStore(): %v", err)
	}
	if !got.Equal(newRun) {
		t.Fatalf("LastRunFuncForStore() = %s, want fresh wisp-tier run %s", got, newRun)
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

func TestCursorFuncForStoreReadsNewestAcrossLimitedTiers(t *testing.T) {
	store := &tierRowsStore{
		MemStore: beads.NewMemStore(),
		rows: map[beads.TierMode][]beads.Bead{
			beads.TierBoth: {{
				ID:     "durable-old",
				Labels: []string{"order-run:digest", "seq:7"},
			}},
			beads.TierIssues: {{
				ID:     "durable-old",
				Labels: []string{"order-run:digest", "seq:7"},
			}},
			beads.TierWisps: {{
				ID:        "wisp-new",
				Labels:    []string{"order-run:digest", "seq:42"},
				Ephemeral: true,
			}},
		},
	}

	if got := CursorFuncForStore(store)("digest"); got != 42 {
		t.Fatalf("CursorFuncForStore() = %d, want fresh wisp-tier seq 42", got)
	}
}
