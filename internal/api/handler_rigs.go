package api

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	gitpkg "github.com/gastownhall/gascity/internal/git"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/suspensionstate"
	workdirutil "github.com/gastownhall/gascity/internal/workdir"
)

type rigResponse struct {
	Name          string     `json:"name"`
	Path          string     `json:"path"`
	Suspended     bool       `json:"suspended"`
	Prefix        string     `json:"prefix,omitempty"`
	DefaultBranch string     `json:"default_branch,omitempty"`
	AgentCount    int        `json:"agent_count"`
	RunningCount  int        `json:"running_count"`
	LastActivity  *time.Time `json:"last_activity,omitempty"`
	Git           *gitStatus `json:"git,omitempty"`
}

type gitStatus struct {
	Branch       string `json:"branch"`
	Clean        bool   `json:"clean"`
	ChangedFiles int    `json:"changed_files"`
	Ahead        int    `json:"ahead"`
	Behind       int    `json:"behind"`
}

// rigRuntimeSnapshot is one cache-first read of the controller's session
// projection. Rig list/get used to probe the runtime provider independently for
// every configured slot (and then repeat every probe for suspension), turning a
// 30-slot Kubernetes city into hundreds of API calls. The session projection
// already carries state and activity and is guarded by the handler's cache-live
// check, so it is the normal read path. Provider ListRunning is a one-call
// fallback for bootstrap/test states with no projected sessions yet.
type rigRuntimeSnapshot struct {
	sessionInfos        []session.Info
	hasSessionReadModel bool
	runningNames        map[string]struct{}
	provider            runtime.Provider
}

func loadRigRuntimeSnapshot(store beads.SessionStore, sp runtime.Provider) rigRuntimeSnapshot {
	if store.Store != nil {
		if infos, _, err := sessionReadModelInfos(session.NewStore(store)); err == nil && len(infos) > 0 {
			return rigRuntimeSnapshot{sessionInfos: infos, hasSessionReadModel: true, provider: sp}
		}
	}
	snapshot := rigRuntimeSnapshot{runningNames: make(map[string]struct{}), provider: sp}
	if sp == nil {
		return snapshot
	}
	if names, err := sp.ListRunning(""); err == nil {
		for _, name := range names {
			if name = strings.TrimSpace(name); name != "" {
				snapshot.runningNames[name] = struct{}{}
			}
		}
	}
	return snapshot
}

func (s rigRuntimeSnapshot) ListRunning(prefix string) ([]string, error) {
	out := make([]string, 0, len(s.runningNames))
	for name := range s.runningNames {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out, nil
}

func (s rigRuntimeSnapshot) hasRunning(name string) bool {
	_, ok := s.runningNames[strings.TrimSpace(name)]
	return ok
}

// buildRigResponse creates a rigResponse from one shared runtime snapshot.
func (s *Server) buildRigResponse(cfg *config.City, rig config.Rig, snapshot rigRuntimeSnapshot, cityName, cityPath string) rigResponse {
	tmpl := cfg.Workspace.SessionTemplate
	var agentCount, runningCount int
	var maxActivity time.Time

	for _, a := range cfg.Agents {
		if workdirutil.ConfiguredRigName(cityPath, a, cfg.Rigs) != rig.Name {
			continue
		}
		expanded := expandAgent(a, cityName, tmpl, snapshot)
		if len(expanded) == 0 {
			// An unlimited pool with no live instances is still one configured
			// agent; the old provider-discovery path returned zero only because
			// it conflated configured capacity with current liveness.
			agentCount++
			continue
		}
		for _, ea := range expanded {
			agentCount++
			if !snapshot.hasSessionReadModel &&
				snapshot.hasRunning(agent.SessionNameFor(cityName, ea.qualifiedName, tmpl)) {
				runningCount++
			}
		}
	}
	if snapshot.hasSessionReadModel {
		seen := make(map[string]struct{})
		for _, info := range snapshot.sessionInfos {
			if info.Closed || !sessionInfoBelongsToRig(cfg, cityPath, rig.Name, info) {
				continue
			}
			identity := strings.TrimSpace(info.SessionName)
			if identity == "" {
				identity = strings.TrimSpace(info.ID)
			}
			if _, duplicate := seen[identity]; duplicate {
				continue
			}
			seen[identity] = struct{}{}
			if rigSessionStateRunning(info.State) {
				runningCount++
			}
			if info.LastActive.After(maxActivity) {
				maxActivity = info.LastActive
			}
		}
	}

	resp := rigResponse{
		Name:          rig.Name,
		Path:          rig.Path,
		Suspended:     s.rigSuspended(cfg, rig, snapshot, agentCount, cityPath),
		Prefix:        rig.Prefix,
		DefaultBranch: rig.DefaultBranch,
		AgentCount:    agentCount,
		RunningCount:  runningCount,
	}
	if !maxActivity.IsZero() {
		resp.LastActivity = &maxActivity
	}
	return resp
}

func sessionInfoBelongsToRig(cfg *config.City, cityPath, rigName string, info session.Info) bool {
	if configured, ok := findAgent(cfg, info.Template); ok {
		return workdirutil.ConfiguredRigName(cityPath, configured, cfg.Rigs) == rigName
	}
	rig, _ := config.ParseQualifiedName(info.Template)
	return rig == rigName
}

func rigSessionStateRunning(state session.State) bool {
	switch state {
	case session.StateActive, session.StateAwake, session.StateDraining:
		return true
	default:
		return false
	}
}

// rigSuspended computes the effective suspended state for a rig. A rig
// is suspended if the runtime state file records an explicit "suspended"
// preference, if the rig's SuspendedOnStart applies with no overriding
// runtime entry, or if every configured slot has a suspended projected
// session. The deprecated `[[rig]] suspended` field in city.toml is
// intentionally NOT consulted — `gc doctor` surfaces it as a migration
// target.
func (s *Server) rigSuspended(cfg *config.City, rig config.Rig, snapshot rigRuntimeSnapshot, agentCount int, cityPath string) bool {
	if rs, err := suspensionstate.Load(fsys.OSFS{}, cityPath); err == nil &&
		suspensionstate.EffectiveRigSuspended(rs, rig.Name, rig.EffectiveSuspendedOnStart()) {
		return true
	}
	if !snapshot.hasSessionReadModel || agentCount == 0 {
		if snapshot.provider == nil || agentCount == 0 {
			return false
		}
		// Bootstrap/test fallback: before the session read model has any rows,
		// retain the legacy provider-metadata interpretation. The live
		// controller leaves this path as soon as its first session is projected,
		// avoiding the per-slot provider fan-out for established cities.
		suspendedCount := 0
		tmpl := cfg.Workspace.SessionTemplate
		for _, a := range cfg.Agents {
			if workdirutil.ConfiguredRigName(cityPath, a, cfg.Rigs) != rig.Name {
				continue
			}
			processNames := config.AgentProcessNames(cfg, a, exec.LookPath)
			for _, ea := range expandAgent(a, s.state.CityName(), tmpl, snapshot) {
				sessionName := agent.SessionNameFor(s.state.CityName(), ea.qualifiedName, tmpl)
				if observeProviderSession(snapshot.provider, sessionName, processNames).Suspended {
					suspendedCount++
				}
			}
		}
		return suspendedCount == agentCount
	}
	seen := make(map[string]struct{})
	suspendedCount := 0
	for _, info := range snapshot.sessionInfos {
		if info.Closed || !sessionInfoBelongsToRig(cfg, cityPath, rig.Name, info) {
			continue
		}
		identity := strings.TrimSpace(info.SessionName)
		if identity == "" {
			identity = strings.TrimSpace(info.ID)
		}
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		if info.State == session.StateSuspended {
			suspendedCount++
		}
	}
	return len(seen) >= agentCount && suspendedCount == len(seen)
}

// gitStatusTimeout bounds how long git operations can take per rig.
const gitStatusTimeout = 3 * time.Second

// fetchGitStatus uses internal/git to get branch/status/ahead-behind info.
// Returns nil on any error or timeout (rig may not be a git repo).
// The context-based timeout ensures that git subprocesses are killed on
// expiry, preventing goroutine and process leaks.
func fetchGitStatus(path string) *gitStatus {
	ctx, cancel := context.WithTimeout(context.Background(), gitStatusTimeout)
	defer cancel()
	return fetchGitStatusCtx(ctx, path)
}

func fetchGitStatusCtx(ctx context.Context, path string) *gitStatus {
	g := gitpkg.New(path)
	if !g.IsRepoCtx(ctx) {
		return nil
	}

	branch, err := g.CurrentBranchCtx(ctx)
	if err != nil {
		return nil
	}

	porcelain, err := g.StatusPorcelainCtx(ctx)
	if err != nil {
		return nil
	}

	var changedFiles int
	for _, line := range strings.Split(porcelain, "\n") {
		if strings.TrimSpace(line) != "" {
			changedFiles++
		}
	}

	gs := &gitStatus{
		Branch:       branch,
		Clean:        changedFiles == 0,
		ChangedFiles: changedFiles,
	}

	// Ahead/behind (best-effort — fails if no upstream set).
	ahead, behind, err := g.AheadBehindCtx(ctx)
	if err == nil {
		gs.Ahead = ahead
		gs.Behind = behind
	}

	return gs
}
