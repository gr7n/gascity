//go:build gascity_native_beads

package beads

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkDoltliteLastOrderRun(b *testing.B) {
	for _, orderCount := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("orders=%d/exact_list", orderCount), func(b *testing.B) {
			store, cleanup, names := benchmarkDoltliteOrderRunStore(b, orderCount)
			defer cleanup()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, name := range names {
					rows, err := store.List(ListQuery{
						Label:         "order-run:" + name,
						Limit:         1,
						IncludeClosed: true,
						Sort:          SortCreatedDesc,
						TierMode:      TierBoth,
					})
					if err != nil {
						b.Fatal(err)
					}
					if len(rows) == 0 {
						b.Fatalf("missing last run for %s", name)
					}
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(orderCount), "lists/op")
		})

		b.Run(fmt.Sprintf("orders=%d/indexed_per_name_cold", orderCount), func(b *testing.B) {
			store, cleanup, names := benchmarkDoltliteOrderRunStore(b, orderCount)
			defer cleanup()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				store.resetOrderRunCache()
				for _, name := range names {
					last, err := store.LastOrderRun(name)
					if err != nil {
						b.Fatal(err)
					}
					if last.IsZero() {
						b.Fatalf("missing last run for %s", name)
					}
				}
			}
			b.StopTimer()
			b.ReportMetric(1, "index_builds/op")
		})

		b.Run(fmt.Sprintf("orders=%d/snapshot_batch_cold", orderCount), func(b *testing.B) {
			store, cleanup, names := benchmarkDoltliteOrderRunStore(b, orderCount)
			defer cleanup()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				store.resetOrderRunCache()
				lastRuns, err := store.LastOrderRuns()
				if err != nil {
					b.Fatal(err)
				}
				for _, name := range names {
					if lastRuns[name].IsZero() {
						b.Fatalf("missing last run for %s", name)
					}
				}
			}
			b.StopTimer()
			b.ReportMetric(1, "snapshots/op")
		})

		b.Run(fmt.Sprintf("orders=%d/snapshot_batch_warm", orderCount), func(b *testing.B) {
			store, cleanup, names := benchmarkDoltliteOrderRunStore(b, orderCount)
			defer cleanup()
			if _, err := store.LastOrderRuns(); err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				lastRuns, err := store.LastOrderRuns()
				if err != nil {
					b.Fatal(err)
				}
				for _, name := range names {
					if lastRuns[name].IsZero() {
						b.Fatalf("missing last run for %s", name)
					}
				}
			}
			b.StopTimer()
			b.ReportMetric(1, "snapshots/op")
		})
	}
}

func benchmarkDoltliteOrderRunStore(tb testing.TB, orderCount int) (*DoltliteReadStore, func(), []string) {
	tb.Helper()
	dir := tb.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		tb.Fatalf("mkdir beads dir: %v", err)
	}
	meta := []byte(`{"backend":"doltlite","database":"doltlite","dolt_database":"hq"}`)
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), meta, 0o600); err != nil {
		tb.Fatalf("write metadata: %v", err)
	}

	dbDir := filepath.Join(beadsDir, "doltlite")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		tb.Fatalf("mkdir doltlite dir: %v", err)
	}
	dbPath := filepath.Join(dbDir, "hq.db")
	db, err := sql.Open("sqlite", dbPath+"?_busy_timeout=10000")
	if err != nil {
		tb.Fatalf("open doltlite fixture db: %v", err)
	}
	createTestDoltliteSchema(tb, db)

	base := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	names := make([]string, 0, orderCount)
	for index := 0; index < orderCount; index++ {
		name := fmt.Sprintf("bench/order-%03d", index)
		names = append(names, name)
		tables := doltliteIssueTables
		if index%4 == 0 {
			tables = doltliteWispTables
		}
		insertTestDoltliteIssue(tb, db, tables.issues, tables.labels, tables.deps, testDoltliteIssue{
			ID:        fmt.Sprintf("bench-order-%03d", index),
			Title:     "order:" + name,
			Status:    "closed",
			IssueType: "task",
			CreatedAt: base.Add(time.Duration(index) * time.Second),
			Labels:    []string{"order-run:" + name},
		})
	}
	if err := db.Close(); err != nil {
		tb.Fatalf("close fixture db: %v", err)
	}

	backing := NewBdStore(dir, func(string, string, ...string) ([]byte, error) {
		tb.Fatal("backing bd runner should not be called by doltlite benchmark")
		return nil, nil
	})
	store, err := NewDoltliteReadStore(dir, backing)
	if err != nil {
		tb.Fatalf("NewDoltliteReadStore: %v", err)
	}
	store.resetOrderRunCache()
	return store, func() { _ = store.CloseStore() }, names
}
