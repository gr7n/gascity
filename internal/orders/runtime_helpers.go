package orders

import (
	"log"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

var runtimeHelpersLogf = log.Printf

type lastOrderRunStore interface {
	LastOrderRun(name string) (time.Time, error)
}

type lastOrderRunsStore interface {
	LastOrderRuns() (map[string]time.Time, error)
}

// LastRunFuncForStore returns the latest order-run bead time for one store.
func LastRunFuncForStore(store beads.Store) LastRunFunc {
	return func(name string) (time.Time, error) {
		if store == nil {
			return time.Time{}, nil
		}
		if indexed, ok := store.(lastOrderRunStore); ok {
			return indexed.LastOrderRun(name)
		}
		return LastOrderRunForStore(store, name)
	}
}

// LastOrderRunForStore returns the latest exact order-run bead time for one
// store, without consulting optional store-specific indexes.
func LastOrderRunForStore(store beads.Store, name string) (time.Time, error) {
	if store == nil {
		return time.Time{}, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return time.Time{}, nil
	}
	label := "order-run:" + name
	// Order-run beads land in either tier: the ephemeral tracking bead
	// (wisps) created by the dispatcher and the molecule root (issues)
	// labeled after instantiation. Both carry the order-run label.
	results, err := store.List(beads.ListQuery{
		Label:         label,
		Limit:         1,
		IncludeClosed: true,
		Sort:          beads.SortCreatedDesc,
		TierMode:      beads.TierBoth,
	})
	if err != nil {
		if len(results) == 0 {
			return time.Time{}, err
		}
		runtimeHelpersLogf("orders: last-run lookup partially failed for %s: %v", name, err)
	}
	if len(results) == 0 {
		return time.Time{}, nil
	}
	return results[0].CreatedAt, nil
}

// LastOrderRunsForStore returns the latest exact order-run bead time for every
// order-run label visible to one store, without consulting optional
// store-specific indexes.
func LastOrderRunsForStore(store beads.Store) (map[string]time.Time, error) {
	lastRun := make(map[string]time.Time)
	if store == nil {
		return lastRun, nil
	}
	results, err := store.List(beads.ListQuery{
		AllowScan:     true,
		IncludeClosed: true,
		TierMode:      beads.TierBoth,
	})
	if err != nil {
		if len(results) == 0 {
			return nil, err
		}
		runtimeHelpersLogf("orders: order-run snapshot partially failed: %v", err)
	}
	for _, row := range results {
		for _, label := range row.Labels {
			name, ok := strings.CutPrefix(label, "order-run:")
			if !ok || name == "" {
				continue
			}
			if row.CreatedAt.After(lastRun[name]) {
				lastRun[name] = row.CreatedAt
			}
		}
	}
	return lastRun, nil
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
		label := "order-run:" + name
		results, err := store.List(beads.ListQuery{
			Label:         label,
			Limit:         10,
			IncludeClosed: true,
			Sort:          beads.SortCreatedDesc,
			TierMode:      beads.TierBoth,
		})
		if err != nil {
			if len(results) == 0 {
				runtimeHelpersLogf("orders: cursor lookup failed for %s: %v", name, err)
				return 0
			}
			runtimeHelpersLogf("orders: cursor lookup partially failed for %s: %v", name, err)
		}
		if len(results) == 0 {
			return 0
		}
		labelSets := make([][]string, 0, len(results))
		for _, b := range results {
			labelSets = append(labelSets, b.Labels)
		}
		return MaxSeqFromLabels(labelSets)
	}
}

// orderTrackingLabel marks the lifecycle bead the dispatcher creates for
// order runs. It is not the authoritative last-run source because manual and
// root-only runs can carry order-run labels without this label.
const orderTrackingLabel = "order-tracking"

// LastRunBatch answers repeated last-run lookups through a store-provided
// authoritative order-run index when one exists. Stores without that hot path
// keep the exact per-name "order-run:<name>" lookup, because order-tracking
// lifecycle beads are not a complete substitute for order-run history: manual
// and root-only runs can be newer than the tracking bead.
type LastRunBatch struct{}

// NewLastRunBatch returns a resolver for a single pass over many orders. The
// limit argument is kept for call-site compatibility with the earlier tracking
// window implementation; authoritative stores own their internal cache bounds.
func NewLastRunBatch(_ int) *LastRunBatch {
	return &LastRunBatch{}
}

// AcrossStores returns a LastRunFunc with LastRunAcrossStores semantics
// (most recent run time across the stores).
func (b *LastRunBatch) AcrossStores(stores ...beads.Store) LastRunFunc {
	type snapshot struct {
		lastRun map[string]time.Time
	}
	snapshots := make([]snapshot, 0, len(stores))
	fallbacks := make([]LastRunFunc, 0, len(stores))
	var snapshotErr error
	for _, store := range stores {
		if store == nil {
			continue
		}
		if indexed, ok := store.(lastOrderRunsStore); ok {
			lastRun, err := indexed.LastOrderRuns()
			if err != nil && snapshotErr == nil {
				snapshotErr = err
			}
			snapshots = append(snapshots, snapshot{lastRun: lastRun})
			continue
		}
		fallbacks = append(fallbacks, LastRunFuncForStore(store))
	}

	return func(name string) (time.Time, error) {
		if snapshotErr != nil {
			return time.Time{}, snapshotErr
		}
		var latest time.Time
		for _, snap := range snapshots {
			if snap.lastRun == nil {
				continue
			}
			if last := snap.lastRun[name]; last.After(latest) {
				latest = last
			}
		}
		for _, fn := range fallbacks {
			last, err := fn(name)
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
