package api

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/orders"
)

func TestParseOrdersFeedLimitCapsLargeValues(t *testing.T) {
	if got := parseOrdersFeedLimit(""); got != 50 {
		t.Fatalf("default limit = %d, want 50", got)
	}
	if got := parseOrdersFeedLimit("25"); got != 25 {
		t.Fatalf("parsed limit = %d, want 25", got)
	}
	if got := parseOrdersFeedLimit("999999"); got != maxOrdersFeedLimit {
		t.Fatalf("capped limit = %d, want %d", got, maxOrdersFeedLimit)
	}
}

func TestOrderTrackingStatusTreatsWispFailedAsFailed(t *testing.T) {
	run, ok := orders.RunFromTrackingBead(beads.Bead{
		Status: "closed",
		Labels: []string{"order-tracking", "order-run:nightly", "wisp", "wisp-failed"},
	})
	if !ok {
		t.Fatal("RunFromTrackingBead ok = false")
	}
	if got := run.State(); got != "failed" {
		t.Fatalf("run.State() = %q, want failed", got)
	}
}

func TestOrderTrackingExecEnvFailedClassifiesAsFailedExec(t *testing.T) {
	run, ok := orders.RunFromTrackingBead(beads.Bead{
		Status: "closed",
		Labels: []string{"order-tracking", "order-run:nightly", "exec-env-failed"},
	})
	if !ok {
		t.Fatal("RunFromTrackingBead ok = false")
	}
	if got := run.State(); got != "failed" {
		t.Fatalf("run.State() = %q, want failed", got)
	}
	if got := orderTrackingTarget(orders.Order{}, false, run); got != "exec" {
		t.Fatalf("orderTrackingTarget = %q, want exec", got)
	}
	if got := orderTrackingType(orders.Order{}, false, run); got != "exec" {
		t.Fatalf("orderTrackingType = %q, want exec", got)
	}
}

func TestWorkflowProjectionTargetKeepsRunTargetMigrationFallback(t *testing.T) {
	root := beads.Bead{Metadata: map[string]string{
		"gc.run_target": "gascity/reviewer",
	}}
	if got := workflowProjectionTarget(root); got != "gascity/reviewer" {
		t.Fatalf("workflowProjectionTarget = %q, want gc.run_target fallback", got)
	}
}

func TestOrderTrackingTriggerEnvFailedClassifiesOpenAndClosedAsFailed(t *testing.T) {
	for _, status := range []string{"open", "closed"} {
		t.Run(status, func(t *testing.T) {
			run, ok := orders.RunFromTrackingBead(beads.Bead{
				Status: status,
				Labels: []string{"order-tracking", "order-run:nightly", "trigger-env-failed"},
			})
			if !ok {
				t.Fatal("RunFromTrackingBead ok = false")
			}
			if got := run.State(); got != "failed" {
				t.Fatalf("run.State(%s) = %q, want failed", status, got)
			}
		})
	}
}

func TestParseMonitorTimestampAcceptsRFC3339AndNano(t *testing.T) {
	base := "2026-03-26T14:06:31+01:00"
	if got := parseMonitorTimestamp(base); got.IsZero() {
		t.Fatalf("parseMonitorTimestamp(%q) = zero, want parsed timestamp", base)
	}

	nano := "2026-03-26T14:06:31.123456789+01:00"
	got := parseMonitorTimestamp(nano)
	if got.IsZero() {
		t.Fatalf("parseMonitorTimestamp(%q) = zero, want parsed timestamp", nano)
	}
	if got.Nanosecond() != 123456789 {
		t.Fatalf("nanoseconds = %d, want 123456789", got.Nanosecond())
	}
	if got.Format("2006-01-02T15:04:05.999999999Z07:00") != nano {
		t.Fatalf("formatted timestamp = %q, want %q", got.Format("2006-01-02T15:04:05.999999999Z07:00"), nano)
	}
}

func TestBuildWorkflowRunProjectionsKeepsInProgressChildrenOnHistoryFailure(t *testing.T) {
	state := newFakeState(t)
	mem := beads.NewMemStore()
	state.stores = map[string]beads.Store{
		"myrig": &workflowProjectionStore{MemStore: mem},
	}

	root, err := mem.Create(beads.Bead{
		Title: "Deploy",
		Type:  "workflow",
		Metadata: map[string]string{
			"gc.kind":             "workflow",
			"gc.formula_contract": "graph.v2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	child, err := mem.Create(beads.Bead{
		Title:    "Run step",
		Type:     "task",
		Assignee: "agent/alice",
		Metadata: map[string]string{
			"gc.root_bead_id": root.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := "in_progress"
	if err := mem.Update(child.ID, beads.UpdateOpts{Status: &status}); err != nil {
		t.Fatal(err)
	}

	got, err := buildWorkflowRunProjections(state, "rig", "myrig", "")
	if err != nil {
		t.Fatalf("buildWorkflowRunProjections: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(got.Items))
	}
	if got.Items[0].Status != "active" {
		t.Fatalf("status = %q, want active", got.Items[0].Status)
	}
	if !got.Items[0].UpdatedAt.Equal(child.CreatedAt) {
		t.Fatalf("updatedAt = %s, want %s", got.Items[0].UpdatedAt, child.CreatedAt)
	}
}

func TestBuildOrderRunFeedItemsUsesAllOrdersForDisabledExecMetadata(t *testing.T) {
	state := newFakeState(t)
	state.cityBeadStore = beads.NewMemStore()
	disabled := false
	state.allOrders = []orders.Order{
		{Name: "digest", Exec: "scripts/digest.sh", Trigger: "cooldown", Interval: "1h", Enabled: &disabled},
	}

	tracking, err := state.cityBeadStore.Create(beads.Bead{
		Title:  "order:digest",
		Status: "closed",
		Labels: []string{"order-tracking", "order-run:digest", "wisp"},
	})
	if err != nil {
		t.Fatalf("create tracking bead: %v", err)
	}

	got, err := buildOrderRunFeedItems(state, "city", "test-city")
	if err != nil {
		t.Fatalf("buildOrderRunFeedItems: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(got.Items))
	}
	item := got.Items[0]
	if item.BeadID != tracking.ID {
		t.Fatalf("bead_id = %q, want %q", item.BeadID, tracking.ID)
	}
	if item.Type != "exec" || item.Target != "exec" || !item.DetailAvailable || !item.RunDetailAvailable {
		t.Fatalf("item = %+v, want disabled exec order metadata", item)
	}
}

func TestLatestOrderRunTimesLogsLookupFailure(t *testing.T) {
	store := labelPrefixFailListStore{
		Store:      beads.NewMemStore(),
		failPrefix: "order-run:",
	}
	front := orders.NewStore(beads.OrdersStore{Store: store})
	tracking := orders.OrderRun{
		Scoped:    "digest",
		CreatedAt: time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC),
	}

	var logs strings.Builder
	origLogf := orderFeedLogf
	orderFeedLogf = func(format string, args ...any) {
		logs.WriteString(strings.TrimSpace(fmt.Sprintf(format, args...)))
		logs.WriteByte('\n')
	}
	defer func() { orderFeedLogf = origLogf }()

	runTimes := latestOrderRunTimes(front, "myrig")
	if len(runTimes) != 0 {
		t.Fatalf("runTimes = %v, want empty on scan failure", runTimes)
	}
	if got := orderTrackingUpdatedAt(tracking, runTimes); !got.Equal(tracking.CreatedAt) {
		t.Fatalf("updatedAt = %s, want %s", got, tracking.CreatedAt)
	}
	if !strings.Contains(logs.String(), "order feed run scan failed") {
		t.Fatalf("logs = %q, want run scan failure warning", logs.String())
	}
}

func TestBuildOrderRunFeedItemsUsesLatestRunForUpdatedAt(t *testing.T) {
	state := newFakeState(t)
	state.cityBeadStore = beads.NewMemStore()
	state.allOrders = []orders.Order{
		{Name: "digest", Exec: "scripts/digest.sh", Trigger: "cooldown", Interval: "1h"},
	}

	tracking, err := state.cityBeadStore.Create(beads.Bead{
		Title:  "order:digest",
		Labels: []string{"order-tracking", "order-run:digest", "wisp"},
	})
	if err != nil {
		t.Fatalf("create tracking bead: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	run, err := state.cityBeadStore.Create(beads.Bead{
		Title:  "order run",
		Labels: []string{"order-run:digest", "wisp"},
	})
	if err != nil {
		t.Fatalf("create run bead: %v", err)
	}

	got, err := buildOrderRunFeedItems(state, "city", "test-city")
	if err != nil {
		t.Fatalf("buildOrderRunFeedItems: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(got.Items))
	}
	item := got.Items[0]
	if item.BeadID != tracking.ID {
		t.Fatalf("bead_id = %q, want %q", item.BeadID, tracking.ID)
	}
	wantUpdated := run.CreatedAt.Format(time.RFC3339Nano)
	if item.UpdatedAt != wantUpdated {
		t.Fatalf("updated_at = %q, want run bead time %q", item.UpdatedAt, wantUpdated)
	}
	if item.StartedAt != tracking.CreatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("started_at = %q, want tracking bead time %q", item.StartedAt, tracking.CreatedAt.Format(time.RFC3339Nano))
	}
}

func TestBuildOrderRunFeedItemsBatchesOrderRunLookups(t *testing.T) {
	const orderCount = 500
	store := &listCountingStore{MemStore: beads.NewMemStore()}
	state := newFakeState(t)
	state.cityBeadStore = store
	state.stores = nil

	allOrders := make([]orders.Order, 0, orderCount)
	for i := 0; i < orderCount; i++ {
		name := fmt.Sprintf("order-%04d", i)
		allOrders = append(allOrders, orders.Order{Name: name, Formula: "review"})
		if _, err := store.Create(beads.Bead{
			Title:  "order:" + name,
			Labels: []string{"order-tracking", "order-run:" + name, "wisp"},
		}); err != nil {
			t.Fatalf("create tracking bead: %v", err)
		}
		if _, err := store.Create(beads.Bead{
			Title:  "run",
			Labels: []string{"order-run:" + name, "wisp"},
		}); err != nil {
			t.Fatalf("create run bead: %v", err)
		}
	}
	state.allOrders = allOrders
	store.lists = 0

	got, err := buildOrderRunFeedItems(state, "city", workflowCityScopeRef(state.CityName()))
	if err != nil {
		t.Fatalf("buildOrderRunFeedItems: %v", err)
	}
	if len(got.Items) != orderCount {
		t.Fatalf("items = %d, want %d", len(got.Items), orderCount)
	}
	if store.lists > 3 {
		t.Fatalf("store List calls = %d, want <= 3", store.lists)
	}
}

type workflowProjectionStore struct {
	*beads.MemStore
}

type labelPrefixFailListStore struct {
	beads.Store
	failPrefix string
}

func (s labelPrefixFailListStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	if query.LabelPrefix == s.failPrefix {
		return nil, errors.New("list failed")
	}
	return s.Store.List(query)
}

// listCountingStore counts List calls so tests can guard against rebuilding the
// order feed with one backing query per tracked order.
type listCountingStore struct {
	*beads.MemStore
	lists int
}

func (s *listCountingStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	s.lists++
	return s.MemStore.List(query)
}

func (s *workflowProjectionStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	if query.IncludeClosed && query.Metadata["gc.root_bead_id"] != "" {
		return nil, errors.New("history unavailable")
	}
	return s.MemStore.List(query)
}
