package orders

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

func BenchmarkLastRunBatchResolvers(b *testing.B) {
	for _, orderCount := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("orders=%d/exact_store_list", orderCount), func(b *testing.B) {
			store, _, names := benchmarkLastRunStores(b, orderCount)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				fn := NewLastRunBatch(orderCount).AcrossStores(store)
				for _, name := range names {
					if _, err := fn(name); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.StopTimer()
			store.report(b)
		})

		b.Run(fmt.Sprintf("orders=%d/indexed_store", orderCount), func(b *testing.B) {
			_, store, names := benchmarkLastRunStores(b, orderCount)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				fn := NewLastRunBatch(orderCount).AcrossStores(store)
				for _, name := range names {
					if _, err := fn(name); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.StopTimer()
			store.report(b)
		})
	}
}

func benchmarkLastRunStores(tb testing.TB, orderCount int) (*lastRunBenchmarkStore, *indexedLastRunBenchmarkStore, []string) {
	tb.Helper()
	mem := beads.NewMemStore()
	names := make([]string, 0, orderCount)
	lastRun := make(map[string]time.Time, orderCount)
	base := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	for index := 0; index < orderCount; index++ {
		name := fmt.Sprintf("order-%03d", index)
		createdAt := base.Add(time.Duration(index) * time.Second)
		names = append(names, name)
		lastRun[name] = createdAt
		if _, err := mem.Create(beads.Bead{
			Title:     "order:" + name,
			Status:    "closed",
			CreatedAt: createdAt,
			Labels:    []string{"order-run:" + name},
		}); err != nil {
			tb.Fatalf("create run %s: %v", name, err)
		}
	}

	exact := &lastRunBenchmarkStore{Store: mem}
	indexed := &indexedLastRunBenchmarkStore{
		lastRunBenchmarkStore: &lastRunBenchmarkStore{Store: mem},
		lastRun:               lastRun,
	}
	return exact, indexed, names
}

type lastRunBenchmarkStore struct {
	beads.Store
	listCalls atomic.Int64
}

func (s *lastRunBenchmarkStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	s.listCalls.Add(1)
	return s.Store.List(query)
}

func (s *lastRunBenchmarkStore) report(b *testing.B) {
	b.ReportMetric(float64(s.listCalls.Load())/float64(b.N), "lists/op")
}

type indexedLastRunBenchmarkStore struct {
	*lastRunBenchmarkStore
	lastRun       map[string]time.Time
	indexedCalls  atomic.Int64
	snapshotCalls atomic.Int64
}

func (s *indexedLastRunBenchmarkStore) LastOrderRun(name string) (time.Time, error) {
	s.indexedCalls.Add(1)
	return s.lastRun[name], nil
}

func (s *indexedLastRunBenchmarkStore) LastOrderRuns() (map[string]time.Time, error) {
	s.snapshotCalls.Add(1)
	out := make(map[string]time.Time, len(s.lastRun))
	for name, last := range s.lastRun {
		out[name] = last
	}
	return out, nil
}

func (s *indexedLastRunBenchmarkStore) report(b *testing.B) {
	s.lastRunBenchmarkStore.report(b)
	b.ReportMetric(float64(s.indexedCalls.Load())/float64(b.N), "indexed_calls/op")
	b.ReportMetric(float64(s.snapshotCalls.Load())/float64(b.N), "snapshots/op")
}
