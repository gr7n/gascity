package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gastownhall/gascity/internal/beads/contract"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
)

const postgresScopeManifestSchema = "gascity.beads-postgres-scopes.v2"

var (
	postgresSchemaName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	workScopeIDName    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	workRepository     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	workIssuePrefix    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,15}$`)
)

type postgresScopeManifest struct {
	Schema string                                `json:"schema"`
	City   postgresScopeManifestEntry            `json:"city"`
	Rigs   map[string]postgresScopeManifestEntry `json:"rigs"`
}

type postgresScopeManifestEntry struct {
	ScopeID        string  `json:"scope_id"`
	Repository     string  `json:"repository"`
	Root           string  `json:"root"`
	GasCityRig     *string `json:"gascity_rig"`
	IssuePrefix    string  `json:"issue_prefix"`
	PostgresDSN    string  `json:"postgres_dsn"`
	PostgresSchema string  `json:"postgres_schema"`
	ProjectID      string  `json:"project_id"`
}

// applyPostgresScopeManifest installs password-free native Beads metadata for
// the city and every configured rig before lifecycle classification. The
// manifest is an immutable runtime input; credentials remain environment-only.
func applyPostgresScopeManifest(cityPath string, cfg *config.City) error {
	manifestPath := strings.TrimSpace(os.Getenv("GC_BEADS_POSTGRES_SCOPE_MANIFEST"))
	if manifestPath == "" {
		return nil
	}
	info, err := os.Lstat(manifestPath)
	if err != nil {
		return fmt.Errorf("reading postgres scope manifest: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return fmt.Errorf("postgres scope manifest must be a regular file no larger than 64 KiB")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading postgres scope manifest: %w", err)
	}
	var manifest postgresScopeManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("decoding postgres scope manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("postgres scope manifest contains trailing JSON")
	}
	if manifest.Schema != postgresScopeManifestSchema {
		return fmt.Errorf("postgres scope manifest schema %q is unsupported", manifest.Schema)
	}
	if cfg == nil {
		return fmt.Errorf("postgres scope manifest requires city config")
	}
	if manifest.City.GasCityRig != nil {
		return fmt.Errorf("postgres scope manifest city entry must not name a rig")
	}
	resolveRigPaths(cityPath, cfg.Rigs)
	if err := installPostgresScopeMetadata(cityPath, manifest.City); err != nil {
		return fmt.Errorf("installing city postgres metadata: %w", err)
	}
	wantRigs := make(map[string]struct{}, len(cfg.Rigs))
	for i := range cfg.Rigs {
		rig := &cfg.Rigs[i]
		wantRigs[rig.Name] = struct{}{}
		entry, ok := manifest.Rigs[rig.Name]
		if !ok {
			return fmt.Errorf("postgres scope manifest is missing configured rig %q", rig.Name)
		}
		if entry.GasCityRig == nil || *entry.GasCityRig != rig.Name {
			return fmt.Errorf("postgres scope manifest rig identity differs for %q", rig.Name)
		}
		if err := installPostgresScopeMetadata(rig.Path, entry); err != nil {
			return fmt.Errorf("installing rig %q postgres metadata: %w", rig.Name, err)
		}
	}
	for name := range manifest.Rigs {
		if _, ok := wantRigs[name]; !ok {
			return fmt.Errorf("postgres scope manifest contains unknown rig %q", name)
		}
	}
	return nil
}

func installPostgresScopeMetadata(scopeRoot string, entry postgresScopeManifestEntry) error {
	scopeID := strings.TrimSpace(entry.ScopeID)
	repository := strings.TrimSpace(entry.Repository)
	root := filepath.Clean(strings.TrimSpace(entry.Root))
	issuePrefix := strings.TrimSpace(entry.IssuePrefix)
	dsn := strings.TrimSpace(entry.PostgresDSN)
	schema := strings.TrimSpace(entry.PostgresSchema)
	projectID := strings.TrimSpace(entry.ProjectID)
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.User == nil || parsed.User.Username() == "" {
		return fmt.Errorf("postgres_dsn is invalid")
	}
	if _, hasPassword := parsed.User.Password(); hasPassword {
		return fmt.Errorf("postgres_dsn must not contain a password")
	}
	if !workScopeIDName.MatchString(scopeID) ||
		!workRepository.MatchString(repository) ||
		root != filepath.Clean(scopeRoot) || !workIssuePrefix.MatchString(issuePrefix) ||
		!postgresSchemaName.MatchString(schema) || projectID == "" {
		return fmt.Errorf("postgres_schema or project_id is invalid")
	}
	if info, err := os.Stat(scopeRoot); err != nil || !info.IsDir() {
		return fmt.Errorf("scope root %s is absent or not a directory", scopeRoot)
	}
	metadataPath := filepath.Join(scopeRoot, ".beads", "metadata.json")
	if existing, ok, err := contract.LoadMetadataState(fsys.OSFS{}, metadataPath); err != nil {
		return err
	} else if ok && existing.Backend == "postgres" {
		if existing.PostgresDSN != dsn || existing.PostgresSchema != schema {
			return fmt.Errorf("existing postgres metadata differs from immutable scope manifest")
		}
	} else if ok && os.Getenv("GC_BEADS_POSTGRES_CUTOVER") != "1" {
		return fmt.Errorf("refusing to replace %s metadata without GC_BEADS_POSTGRES_CUTOVER=1", existing.Backend)
	}
	if err := os.MkdirAll(filepath.Join(scopeRoot, ".beads"), 0o755); err != nil {
		return err
	}
	if err := contract.WriteProjectIdentity(fsys.OSFS{}, scopeRoot, projectID); err != nil {
		return err
	}
	_, err = contract.EnsureCanonicalMetadata(fsys.OSFS{}, metadataPath, contract.MetadataState{
		Database: "beads", Backend: "postgres", PostgresDSN: dsn, PostgresSchema: schema,
	})
	if err != nil {
		return err
	}
	return removeScopeLocalDoltServerArtifacts(scopeRoot)
}
