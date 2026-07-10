package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
)

// BenchmarkOrdersFeedUnderIndexChurn measures one dashboard poll of
//
//	GET /v0/city/{city}/orders/feed?scope_kind=city&scope_ref={city}
//
// through the real HTTP mux, on a city whose event sequence advances between
// polls ("index churn") — the normal state of any city with active agents,
// since every bead create/update/close advances the sequence.
//
// # Why churn matters
//
// The feed's response cache used to be keyed on the event index, so any event
// between two polls invalidated the cached body and the next poll rebuilt it
// from store scans. The fix keys the cache on a wall-clock time bucket
// (responseCacheTimeBucket, the same lever as /status and /formulas/feed), so
// churn no longer forces rebuilds; the built body is reused until the bucket
// rolls over.
//
// Each iteration records one event, then issues one GET. Two variants:
//
//   - time-bucket-cache: the current behavior. timeBucketResponseCacheTTL is
//     pinned wide (1h) so polls share one bucket; rebuilds happen only when
//     the response-entry TTL (responseCacheTTL, 2s) expires.
//   - rebuild-per-poll: the previous behavior, reproduced by pinning the
//     bucket TTL to 1ns so each poll lands in a new bucket and rebuilds —
//     exactly what the index-keyed cache did under churn.
//
// # What a rebuild costs
//
// The fixture seeds 100 graph.v2 workflow roots (each with one child step) in
// one rig store, and 50 order-tracking beads in the city store. One rebuild
// then issues:
//
//	rig store:  1 active scan + 1 closed-roots scan + 100 per-root child
//	            lookups + 1 order-tracking list            = 103 List calls
//	city store: 1 active scan + 1 closed-roots scan + 1 order-tracking
//	            list + 50 per-order latest-run lookups     =  53 List calls
//	                                                  total = 156 List calls
//
// storeList/op reports those backing-store List calls per poll. That count is
// the operationally relevant figure: this benchmark backs the stores with
// in-memory MemStore (microseconds per List), but on a BdStore-backed city
// every List is a `bd list` subprocess — fork/exec, a fresh SQL connection,
// and a table scan — so wall-clock numbers here are a strict lower bound on
// the real cost. The List-call count itself is backend-independent.
//
// # Reading the results
//
// Expected shape (timings vary by machine; counts do not):
//
//	time-bucket-cache:  ~0.02 storeList/op (rebuilds only at TTL expiry)
//	rebuild-per-poll:   156   storeList/op (full rebuild every poll)
//
// Run with:
//
//	go test ./internal/api/ -run XXX -bench OrdersFeedUnderIndexChurn -benchtime 2s
func BenchmarkOrdersFeedUnderIndexChurn(b *testing.B) {
	const workflowRoots = 100
	const trackedOrders = 50

	cases := []struct {
		name string
		ttl  time.Duration
	}{
		{"time-bucket-cache", time.Hour},
		{"rebuild-per-poll", time.Nanosecond},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			oldTTL := timeBucketResponseCacheTTL
			timeBucketResponseCacheTTL = tc.ttl
			b.Cleanup(func() { timeBucketResponseCacheTTL = oldTTL })

			fs := newFakeState(b)
			rig := &benchListCountingStore{Store: fs.stores["myrig"]}
			city := &benchListCountingStore{Store: beads.NewMemStore()}
			fs.stores["myrig"] = rig
			fs.cityBeadStore = city
			seedOrdersFeedFixture(b, rig, city, workflowRoots, trackedOrders)

			// Same construction as newTestCityHandler, inlined because that
			// helper requires *testing.T.
			h := wrapTestSupervisorMiddleware(NewSupervisorMux(&stateCityResolver{state: fs}, nil, false, "test", "", time.Now()))
			req := httptest.NewRequest(http.MethodGet, cityURL(fs, "/orders/feed?scope_kind=city&scope_ref=test-city"), nil)

			// Warm once so both variants start from a built body and the
			// measured loop reflects steady-state polling, not first-build.
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatalf("warm feed = %d, want 200; body = %s", rec.Code, rec.Body.String())
			}

			warmCalls := rig.listCalls + city.listCalls
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// One event between polls is the minimum churn that busted
				// the old index-keyed cache; real cities produce many.
				fs.eventProv.Record(events.Event{Type: events.BeadCreated, Actor: "bench"})
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					b.Fatalf("feed poll %d = %d, want 200", i, rec.Code)
				}
			}
			b.StopTimer()
			total := rig.listCalls + city.listCalls - warmCalls
			b.ReportMetric(float64(total)/float64(b.N), "storeList/op")
		})
	}
}

// benchListCountingStore counts every List call that reaches the backing
// store, regardless of filter shape, so the benchmark can report backend
// load per poll.
type benchListCountingStore struct {
	beads.Store
	listCalls int
}

func (s *benchListCountingStore) List(q beads.ListQuery) ([]beads.Bead, error) {
	s.listCalls++
	return s.Store.List(q)
}

// seedOrdersFeedFixture populates the stores with the two bead shapes the
// feed renders:
//
//   - rig store: `roots` graph.v2 workflow roots, each with one child step
//     carrying gc.root_bead_id. The workflow projection scans these and does
//     one child List per root.
//   - city store: `tracked` order-tracking beads, each labeled
//     order-run:<name>. The order-run section lists these and does one
//     latest-run List per order.
//
// 100 roots / 50 orders models a city that has been running workflows and
// scheduled orders for a while. Both counts scale the per-rebuild List total
// linearly, so different values change magnitude, not shape.
func seedOrdersFeedFixture(b *testing.B, rig, city beads.Store, roots, tracked int) {
	b.Helper()
	for i := 0; i < roots; i++ {
		root, err := rig.Create(beads.Bead{
			Title: fmt.Sprintf("workflow %d", i),
			Ref:   "mol-bench-v2",
			Metadata: map[string]string{
				"gc.kind":             "workflow",
				"gc.formula_contract": "graph.v2",
				"gc.workflow_id":      fmt.Sprintf("wf-%d", i),
				"gc.scope_kind":       "rig",
				"gc.scope_ref":        "myrig",
			},
		})
		if err != nil {
			b.Fatalf("create workflow root %d: %v", i, err)
		}
		if _, err := rig.Create(beads.Bead{
			Title:    fmt.Sprintf("step %d", i),
			Metadata: map[string]string{"gc.root_bead_id": root.ID},
		}); err != nil {
			b.Fatalf("create workflow child %d: %v", i, err)
		}
	}
	for i := 0; i < tracked; i++ {
		if _, err := city.Create(beads.Bead{
			Title:  fmt.Sprintf("order:bench-%d", i),
			Labels: []string{"order-tracking", fmt.Sprintf("order-run:bench-%d", i)},
		}); err != nil {
			b.Fatalf("create tracking bead %d: %v", i, err)
		}
	}
}
