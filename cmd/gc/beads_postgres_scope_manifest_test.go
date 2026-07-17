package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/beads/contract"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
)

func TestApplyPostgresScopeManifestCutsOverExactConfiguredScopes(t *testing.T) {
	city := t.TempDir()
	rig := filepath.Join(city, "greenomes")
	if err := os.MkdirAll(filepath.Join(rig, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(city, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{city, rig} {
		if err := os.WriteFile(filepath.Join(root, ".beads", "metadata.json"), []byte(`{"database":"dolt","backend":"dolt","dolt_mode":"server","dolt_database":"old"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dsn := "postgresql://beads_runtime@postgres.example:5432/gr7n_beads"
	manifest := postgresScopeManifest{
		Schema: postgresScopeManifestSchema,
		City:   postgresScopeManifestEntry{PostgresDSN: dsn, PostgresSchema: "company", ProjectID: "company-id"},
		Rigs:   map[string]postgresScopeManifestEntry{"greenomes": {PostgresDSN: dsn, PostgresSchema: "greenomes", ProjectID: "greenomes-id"}},
	}
	path := filepath.Join(t.TempDir(), "scopes.json")
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(path, data, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GC_BEADS_POSTGRES_SCOPE_MANIFEST", path)
	cfg := &config.City{Rigs: []config.Rig{{Name: "greenomes", Path: rig}}}
	if err := applyPostgresScopeManifest(city, cfg); err == nil {
		t.Fatal("cutover without explicit flag succeeded")
	}
	t.Setenv("GC_BEADS_POSTGRES_CUTOVER", "1")
	if err := applyPostgresScopeManifest(city, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for root, wantSchema := range map[string]string{city: "company", rig: "greenomes"} {
		state, ok, err := contract.LoadMetadataState(fsys.OSFS{}, filepath.Join(root, ".beads", "metadata.json"))
		if err != nil || !ok {
			t.Fatalf("load %s: ok=%v err=%v", root, ok, err)
		}
		if state.Backend != "postgres" || state.PostgresDSN != dsn || state.PostgresSchema != wantSchema {
			t.Fatalf("state %s = %+v", root, state)
		}
	}
}

func TestApplyPostgresScopeManifestRejectsUnknownRig(t *testing.T) {
	city := t.TempDir()
	path := filepath.Join(t.TempDir(), "scopes.json")
	manifest := postgresScopeManifest{
		Schema: postgresScopeManifestSchema,
		City:   postgresScopeManifestEntry{PostgresDSN: "postgresql://u@h:5432/d", PostgresSchema: "company", ProjectID: "id"},
		Rigs:   map[string]postgresScopeManifestEntry{"ghost": {PostgresDSN: "postgresql://u@h:5432/d", PostgresSchema: "ghost", ProjectID: "id"}},
	}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(path, data, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GC_BEADS_POSTGRES_SCOPE_MANIFEST", path)
	if err := applyPostgresScopeManifest(city, &config.City{}); err == nil {
		t.Fatal("unknown rig accepted")
	}
}
