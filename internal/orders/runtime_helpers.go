package orders

import (
	"errors"
	"log"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

var runtimeHelpersLogf = log.Printf

// LastRunFuncForStore returns the latest order-run bead time for one store.
func LastRunFuncForStore(store beads.Store) LastRunFunc {
	return func(name string) (time.Time, error) {
		if store == nil {
			return time.Time{}, nil
		}
		latest, err := latestOrderRunAcrossStoreTiers(store, name)
		if err != nil {
			if !latest.IsZero() {
				runtimeHelpersLogf("orders: last-run lookup partially failed for %s: %v", name, err)
				return latest, nil
			}
			return time.Time{}, err
		}
		return latest, nil
	}
}

func latestOrderRunAcrossStoreTiers(store beads.Store, name string) (time.Time, error) {
	var latest time.Time
	var readErr error
	for _, tier := range orderRunLookupTiers() {
		results, err := store.List(orderRunLookupQuery(name, tier, 1))
		if err != nil {
			readErr = errors.Join(readErr, err)
		}
		for _, result := range results {
			if result.CreatedAt.After(latest) {
				latest = result.CreatedAt
			}
		}
	}
	return latest, readErr
}

func orderRunLookupQuery(name string, tier beads.TierMode, limit int) beads.ListQuery {
	return beads.ListQuery{
		Label:         "order-run:" + name,
		Limit:         limit,
		IncludeClosed: true,
		Sort:          beads.SortCreatedDesc,
		TierMode:      tier,
	}
}

func orderRunLookupTiers() []beads.TierMode {
	return []beads.TierMode{beads.TierWisps, beads.TierIssues}
}

// LastRunAcrossStores returns the most recent run time across a set of stores
// for a single order name.
func LastRunAcrossStores(stores ...beads.Store) LastRunFunc {
	return func(name string) (time.Time, error) {
		var latest time.Time
		for _, store := range stores {
			if store == nil {
				continue
			}
			last, err := LastRunFuncForStore(store)(name)
			if err != nil {
				return time.Time{}, err
			}
			if last.After(latest) {
				latest = last
			}
		}
		return latest, nil
	}
}

// CursorFuncForStore returns the max order-run seq for one store.
func CursorFuncForStore(store beads.Store) CursorFunc {
	return func(name string) uint64 {
		if store == nil {
			return 0
		}
		latest, err := latestOrderRunCursorAcrossStoreTiers(store, name)
		if err != nil {
			if latest == 0 {
				runtimeHelpersLogf("orders: cursor lookup failed for %s: %v", name, err)
				return 0
			}
			runtimeHelpersLogf("orders: cursor lookup partially failed for %s: %v", name, err)
		}
		return latest
	}
}

func latestOrderRunCursorAcrossStoreTiers(store beads.Store, name string) (uint64, error) {
	var latest uint64
	var readErr error
	for _, tier := range orderRunLookupTiers() {
		results, err := store.List(orderRunLookupQuery(name, tier, 10))
		if err != nil {
			readErr = errors.Join(readErr, err)
		}
		if len(results) == 0 {
			continue
		}
		labelSets := make([][]string, 0, len(results))
		for _, b := range results {
			labelSets = append(labelSets, b.Labels)
		}
		if seq := MaxSeqFromLabels(labelSets); seq > latest {
			latest = seq
		}
	}
	return latest, readErr
}

// CursorAcrossStores merges seq cursors from multiple stores.
func CursorAcrossStores(stores ...beads.Store) CursorFunc {
	fns := make([]CursorFunc, 0, len(stores))
	for _, store := range stores {
		if store != nil {
			fns = append(fns, CursorFuncForStore(store))
		}
	}
	return func(name string) uint64 {
		var latest uint64
		for _, fn := range fns {
			if seq := fn(name); seq > latest {
				latest = seq
			}
		}
		return latest
	}
}
