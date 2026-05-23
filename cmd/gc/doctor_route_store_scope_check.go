package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
	"github.com/gastownhall/gascity/internal/sling"
)

type routeStoreScopeCheck struct {
	cfg      *config.City
	cityPath string
	newStore func(string) (beads.Store, error)
}

type routeStoreScopeFinding struct {
	storeRef string
	path     string
	bead     beads.Bead
	route    string
	err      *sling.RouteStoreScopeError
}

func newRouteStoreScopeCheck(cfg *config.City, cityPath string, newStore func(string) (beads.Store, error)) *routeStoreScopeCheck {
	return &routeStoreScopeCheck{cfg: cfg, cityPath: cityPath, newStore: newStore}
}

func (c *routeStoreScopeCheck) Name() string { return "route-store-scope" }

func (c *routeStoreScopeCheck) CanFix() bool { return true }

func (c *routeStoreScopeCheck) WarmupEligible() bool { return false }

func (c *routeStoreScopeCheck) Run(_ *doctor.CheckContext) *doctor.CheckResult {
	findings, skipped := c.findings()
	if len(findings) == 0 && len(skipped) == 0 {
		return okCheck(c.Name(), "all routed beads live in the store their target reads")
	}
	details := routeStoreScopeDetails(findings, skipped)
	if len(findings) == 0 {
		return warnCheck(c.Name(),
			fmt.Sprintf("route/store scope check skipped %d scope(s)", len(skipped)),
			"fix bead store access, then rerun gc doctor",
			details)
	}
	return &doctor.CheckResult{
		Name:    c.Name(),
		Status:  doctor.StatusError,
		Message: fmt.Sprintf("%d routed bead(s) live in the wrong Beads store", len(findings)),
		FixHint: "run `gc doctor --check route-store-scope --fix` to migrate active routed beads into their canonical store",
		Details: details,
	}
}

func (c *routeStoreScopeCheck) Fix(_ *doctor.CheckContext) error {
	findings, skipped := c.findings()
	if len(skipped) > 0 {
		return fmt.Errorf("cannot migrate route/store findings while store scans are skipped: %s", strings.Join(skipped, "; "))
	}
	for _, f := range findings {
		dstPath, ok := c.storePathForRef(f.err.Expected)
		if !ok {
			return fmt.Errorf("cannot resolve expected store %s for bead %s", f.err.Expected, f.bead.ID)
		}
		src, err := c.newStore(f.path)
		if err != nil {
			return fmt.Errorf("opening source store %s: %w", f.path, err)
		}
		dst, err := c.newStore(dstPath)
		if err != nil {
			return fmt.Errorf("opening target store %s: %w", dstPath, err)
		}
		clone := cloneBeadForRouteStoreMigration(f.bead, f.storeRef, f.err.Expected)
		created, err := dst.Create(clone)
		if err != nil {
			return fmt.Errorf("creating migrated copy for %s in %s: %w", f.bead.ID, f.err.Expected, err)
		}
		if _, err := src.CloseAll([]string{f.bead.ID}, map[string]string{
			"gc.routed_to":          "",
			"gc.migrated_to_store":  f.err.Expected,
			"gc.migrated_to_bead":   created.ID,
			"gc.migrated_at":        time.Now().UTC().Format(time.RFC3339),
			"gc.migration_reason":   "route-store-scope",
			"gc.canonical_store":    f.err.Expected,
			"gc.previous_routed_to": f.route,
		}); err != nil {
			return fmt.Errorf("closing migrated source bead %s: %w", f.bead.ID, err)
		}
	}
	return nil
}

func (c *routeStoreScopeCheck) findings() ([]routeStoreScopeFinding, []string) {
	var findings []routeStoreScopeFinding
	var skipped []string
	c.scanStore(&findings, &skipped, c.cityPath, cityStoreRefForCheck(c.cfg, c.cityPath))
	if c.cfg != nil {
		for _, rig := range c.cfg.Rigs {
			if rig.Suspended || strings.TrimSpace(rig.Path) == "" {
				continue
			}
			c.scanStore(&findings, &skipped, rig.Path, "rig:"+rig.Name)
		}
	}
	return findings, skipped
}

func (c *routeStoreScopeCheck) scanStore(findings *[]routeStoreScopeFinding, skipped *[]string, path, storeRef string) {
	if c.newStore == nil || strings.TrimSpace(path) == "" {
		return
	}
	store, err := c.newStore(path)
	if err != nil {
		*skipped = append(*skipped, fmt.Sprintf("%s skipped: opening bead store: %v", storeRef, err))
		return
	}
	items, err := store.List(beads.ListQuery{AllowScan: true})
	if err != nil {
		*skipped = append(*skipped, fmt.Sprintf("%s skipped: listing beads: %v", storeRef, err))
		return
	}
	cityName := ""
	if c.cfg != nil {
		cityName = c.cfg.EffectiveCityName()
	}
	for _, bead := range items {
		route := strings.TrimSpace(bead.Metadata["gc.routed_to"])
		if route == "" {
			continue
		}
		if bead.Status == "closed" {
			continue
		}
		if err := sling.CheckRouteStoreScope(route, storeRef, cityName, c.cfg); err != nil {
			*findings = append(*findings, routeStoreScopeFinding{
				storeRef: storeRef,
				path:     path,
				bead:     bead,
				route:    route,
				err:      err,
			})
		}
	}
}

func (c *routeStoreScopeCheck) storePathForRef(storeRef string) (string, bool) {
	kind, name, ok := strings.Cut(strings.TrimSpace(storeRef), ":")
	if !ok {
		return "", false
	}
	switch kind {
	case "city":
		return c.cityPath, true
	case "rig":
		if c.cfg == nil {
			return "", false
		}
		for _, rig := range c.cfg.Rigs {
			if rig.Name == name && strings.TrimSpace(rig.Path) != "" {
				return rig.Path, true
			}
		}
	}
	return "", false
}

func cloneBeadForRouteStoreMigration(src beads.Bead, fromStore, toStore string) beads.Bead {
	metadata := map[string]string{}
	for k, v := range src.Metadata {
		metadata[k] = v
	}
	metadata["gc.migrated_from_store"] = fromStore
	metadata["gc.migrated_from_bead"] = src.ID
	metadata["gc.canonical_store"] = toStore
	metadata["gc.migrated_at"] = time.Now().UTC().Format(time.RFC3339)
	metadata["gc.migration_reason"] = "route-store-scope"
	return beads.Bead{
		Title:       src.Title,
		Status:      "open",
		Type:        src.Type,
		Priority:    src.Priority,
		From:        src.From,
		Description: src.Description,
		Labels:      append([]string{}, src.Labels...),
		Metadata:    metadata,
	}
}

func routeStoreScopeDetails(findings []routeStoreScopeFinding, skipped []string) []string {
	details := make([]string, 0, len(findings)+len(skipped))
	for _, f := range findings {
		details = append(details, fmt.Sprintf("%s bead %s has gc.routed_to=%q; expected %s", f.storeRef, f.bead.ID, f.route, f.err.Expected))
	}
	details = append(details, skipped...)
	sort.Strings(details)
	return details
}

func cityStoreRefForCheck(cfg *config.City, cityPath string) string {
	name := ""
	if cfg != nil {
		name = cfg.EffectiveCityName()
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(cityPath)
	}
	return "city:" + name
}
