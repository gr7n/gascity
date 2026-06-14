package api

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

func BenchmarkOrdersFeedWorkflowProjectionRootOnly(b *testing.B) {
	for _, rootCount := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("roots=%d/full", rootCount), func(b *testing.B) {
			state, store := benchmarkWorkflowProjectionState(b, rootCount, 2)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := buildWorkflowRunProjections(state, "rig", "myrig", ""); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			store.report(b)
		})

		b.Run(fmt.Sprintf("roots=%d/root_only", rootCount), func(b *testing.B) {
			state, store := benchmarkWorkflowProjectionState(b, rootCount, 2)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := buildWorkflowRunProjectionsRootOnly(state, "rig", "myrig"); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			store.report(b)
		})
	}
}

func benchmarkWorkflowProjectionState(tb testing.TB, rootCount, closedChildrenPerRoot int) (*fakeState, *workflowProjectionBenchmarkStore) {
	tb.Helper()
	state := newFakeState(tb)
	mem := beads.NewMemStore()
	store := &workflowProjectionBenchmarkStore{Store: mem}
	state.cityBeadStore = beads.NewMemStore()
	state.stores = map[string]beads.Store{"myrig": store}

	for rootIndex := 0; rootIndex < rootCount; rootIndex++ {
		root, err := mem.Create(beads.Bead{
			Title:  fmt.Sprintf("Workflow %03d", rootIndex),
			Type:   "workflow",
			Status: "in_progress",
			Metadata: map[string]string{
				beadmeta.KindMetadataKey:            "workflow",
				beadmeta.FormulaContractMetadataKey: "graph.v2",
				beadmeta.WorkflowIDMetadataKey:      fmt.Sprintf("wf-%03d", rootIndex),
				beadmeta.ScopeKindMetadataKey:       "rig",
				beadmeta.ScopeRefMetadataKey:        "myrig",
			},
		})
		if err != nil {
			tb.Fatalf("create root %d: %v", rootIndex, err)
		}
		for childIndex := 0; childIndex < closedChildrenPerRoot; childIndex++ {
			child, err := mem.Create(beads.Bead{
				Title:  fmt.Sprintf("Workflow %03d closed child %d", rootIndex, childIndex),
				Type:   "task",
				Status: "closed",
				Metadata: map[string]string{
					beadmeta.RootBeadIDMetadataKey: root.ID,
				},
			})
			if err != nil {
				tb.Fatalf("create child %d/%d: %v", rootIndex, childIndex, err)
			}
			if err := mem.Close(child.ID); err != nil {
				tb.Fatalf("close child %d/%d: %v", rootIndex, childIndex, err)
			}
		}
	}
	return state, store
}

type workflowProjectionBenchmarkStore struct {
	beads.Store
	listCalls         atomic.Int64
	childHistoryCalls atomic.Int64
}

func (s *workflowProjectionBenchmarkStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	s.listCalls.Add(1)
	if query.IncludeClosed && strings.TrimSpace(query.Metadata[beadmeta.RootBeadIDMetadataKey]) != "" {
		s.childHistoryCalls.Add(1)
	}
	return s.Store.List(query)
}

func (s *workflowProjectionBenchmarkStore) report(b *testing.B) {
	b.ReportMetric(float64(s.listCalls.Load())/float64(b.N), "lists/op")
	b.ReportMetric(float64(s.childHistoryCalls.Load())/float64(b.N), "child_history_lists/op")
}
