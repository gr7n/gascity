package tmux

import (
	"slices"
	"testing"
)

func TestProviderEnvSkipsEscapeForPiAlias(t *testing.T) {
	if !providerEnvSkipsEscape("my-pi/tmux") {
		t.Fatal("pi provider alias should skip pre-enter Escape")
	}
}

func TestProviderEnvSkipsEscapeForCopilot(t *testing.T) {
	if !providerEnvSkipsEscape("copilot") {
		t.Fatal("copilot provider should skip pre-enter Escape")
	}
}

// TestProviderEnvFamily locks in the GC_PROVIDER normalization used by the
// tmux behavior gates: known families (including wrapped aliases that
// normalize onto one) resolve to the family name, while custom providers
// whose family the name does not reveal resolve to "" so callers fall back
// to process-tree detection instead of assuming default behavior.
func TestProviderEnvFamily(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"", ""},
		{"codex", "codex"},
		{"codex-mini", "codex"},
		{"gemini", "gemini"},
		{"my-gemini-fast", "gemini"},
		{"kimi", "kimi"},
		{"kimi-pro", "kimi"},
		{"claude", "claude"},
		{"claude-eco", "claude"},
		{"copilot", "copilot"},
		{"grok", "grok"},
		{"my-pi/tmux", "pi"},
		{"antigravity", "antigravity"},
		{"opencode", "opencode"},
		// Custom wrappers: nothing in the name reveals the family. The gate
		// must treat these like an unset GC_PROVIDER (process-tree fallback),
		// not like a provider with known default behavior.
		{"bespoke", ""},
		{"router-exec", ""},
	}
	for _, tt := range tests {
		if got := providerEnvFamily(tt.provider); got != tt.want {
			t.Errorf("providerEnvFamily(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

// TestProviderEnvSkipsEscapeUnknownProvider: a custom provider name must not
// skip the Escape on the strength of the env value alone — the process-tree
// fallback in shouldSendEscapeBeforeEnter makes that call.
func TestProviderEnvSkipsEscapeUnknownProvider(t *testing.T) {
	if providerEnvSkipsEscape("bespoke") {
		t.Fatal("unknown custom provider must not skip pre-enter Escape from env alone")
	}
}

// TestComputeExcludingKillSet_SelfCloseExcludesCallerKeepsAgent locks in the
// fix for the self-close wedge: when `gc session close` runs from inside the
// pane it is tearing down, the caller is a descendant of the pane leader (the
// agent). The caller must be excluded from the TERM list so it survives long
// enough to finish cleanup, while the pane leader (agent) is still reached.
func TestComputeExcludingKillSet_SelfCloseExcludesCallerKeepsAgent(t *testing.T) {
	const (
		agentPID  = "100" // pane leader (e.g. the coding agent) — must be killed
		shellPID  = "101" // intermediate shell spawned by the agent
		callerPID = "102" // gc session close — the excluded caller
	)
	exclude := map[string]bool{callerPID: true}

	killList, killPaneLeader := computeExcludingKillSet(
		agentPID,
		[]string{shellPID, callerPID},
		nil,
		exclude,
	)

	if !killPaneLeader {
		t.Error("pane leader (agent) must be killed, but it was reported excluded")
	}
	if slices.Contains(killList, callerPID) {
		t.Errorf("caller %s must be excluded from TERM list, got %v", callerPID, killList)
	}
	if !slices.Contains(killList, shellPID) {
		t.Errorf("non-excluded descendant %s must be in TERM list, got %v", shellPID, killList)
	}
}

// TestComputeExcludingKillSet_ExternalCallerKillsEverything verifies that when
// the caller lives outside the pane (e.g. the supervisor running the close),
// excluding its PID is a harmless no-op: every process in the pane's tree is
// still terminated.
func TestComputeExcludingKillSet_ExternalCallerKillsEverything(t *testing.T) {
	const agentPID = "200"
	exclude := map[string]bool{"999": true} // external caller, not in the pane tree

	killList, killPaneLeader := computeExcludingKillSet(
		agentPID,
		[]string{"201"},
		[]string{"202"},
		exclude,
	)

	if !killPaneLeader {
		t.Error("pane leader must be killed for an external caller")
	}
	if !slices.Contains(killList, "201") || !slices.Contains(killList, "202") {
		t.Errorf("all pane descendants must be killed, got %v", killList)
	}
}

// TestComputeExcludingKillSet_ExcludedPaneLeaderSurvives guards the degenerate
// case where the pane leader itself is in the exclusion set: it must not be
// signaled directly (the final tmux kill-session reaps it instead).
func TestComputeExcludingKillSet_ExcludedPaneLeaderSurvives(t *testing.T) {
	const agentPID = "300"
	exclude := map[string]bool{agentPID: true}

	_, killPaneLeader := computeExcludingKillSet(agentPID, nil, nil, exclude)

	if killPaneLeader {
		t.Error("an excluded pane leader must not be killed directly")
	}
}
