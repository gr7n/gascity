package session

import (
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestUseAgentTemplateForProviderResolution(t *testing.T) {
	tests := []struct {
		name              string
		kind              string
		metadata          map[string]string
		persistedProvider string
		templateProvider  string
		templateFound     bool
		want              bool
	}{
		{
			name: "explicit provider kind skips agent template",
			kind: "provider",
			want: false,
		},
		{
			name: "explicit agent kind uses agent template",
			kind: "agent",
			want: true,
		},
		{
			name: "legacy nil metadata preserves agent template behavior",
			want: true,
		},
		{
			name: "configured named session uses agent template",
			metadata: map[string]string{
				NamedSessionMetadataKey: "true",
				"session_origin":        "manual",
			},
			want: true,
		},
		{
			name: "manual provider session with template collision skips agent template",
			metadata: map[string]string{
				"session_origin": "manual",
			},
			persistedProvider: "stored-provider",
			templateProvider:  "agent-provider",
			templateFound:     true,
			want:              false,
		},
		{
			name: "manual session with matching provider but no agent metadata stays provider backed",
			metadata: map[string]string{
				"session_origin": "manual",
			},
			persistedProvider: "agent-provider",
			templateProvider:  "agent-provider",
			templateFound:     true,
			want:              false,
		},
		{
			name: "manual session with agent name preserves agent template",
			metadata: map[string]string{
				"agent_name":     "worker",
				"session_origin": "manual",
			},
			persistedProvider: "agent-provider",
			templateProvider:  "agent-provider",
			templateFound:     true,
			want:              true,
		},
		{
			name: "manual session without matching template is provider backed",
			metadata: map[string]string{
				"session_origin": "manual",
			},
			persistedProvider: "agent-provider",
			templateProvider:  "",
			templateFound:     false,
			want:              false,
		},
		{
			name: "non-manual legacy metadata preserves agent template behavior",
			metadata: map[string]string{
				"session_origin": "ephemeral",
			},
			persistedProvider: "agent-provider",
			templateProvider:  "agent-provider",
			templateFound:     true,
			want:              true,
		},
		{
			name: "non-manual legacy metadata with provider mismatch preserves agent template behavior",
			metadata: map[string]string{
				"session_origin": "ephemeral",
			},
			persistedProvider: "stored-provider",
			templateProvider:  "agent-provider",
			templateFound:     true,
			want:              true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UseAgentTemplateForProviderResolution(tt.kind, tt.metadata, tt.persistedProvider, tt.templateProvider, tt.templateFound)
			if got != tt.want {
				t.Fatalf("UseAgentTemplateForProviderResolution() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConcreteAgentIdentityUsesTemplate(t *testing.T) {
	one := 1
	tests := []struct {
		name  string
		info  Info
		agent *config.Agent
		want  bool
	}{
		{
			name:  "controller-managed alias binds concrete identity",
			info:  Info{AgentName: "rig/rw-lifecycle", Alias: "rig/rw-lifecycle"},
			agent: &config.Agent{Name: "worker", Dir: "rig"},
			want:  true,
		},
		{
			name: "canonical record binds concrete identity",
			info: Info{
				AgentName:                     "rig/worker-7",
				CanonicalInstanceNameMetadata: "rig/worker-7",
			},
			agent: &config.Agent{Name: "worker", Dir: "rig"},
			want:  true,
		},
		{
			name:  "different alias does not bind",
			info:  Info{AgentName: "removed-router", Alias: "friendly-session"},
			agent: &config.Agent{Name: "friendly"},
		},
		{
			name: "different canonical identity does not bind",
			info: Info{
				AgentName:                     "removed-router",
				CanonicalInstanceNameMetadata: "friendly-1",
			},
			agent: &config.Agent{Name: "friendly"},
		},
		{
			name:  "single-session template cannot mint concrete identities",
			info:  Info{AgentName: "friendly", Alias: "friendly"},
			agent: &config.Agent{Name: "friendly", MaxActiveSessions: &one},
		},
		{
			name:  "missing agent name",
			info:  Info{Alias: "friendly"},
			agent: &config.Agent{Name: "friendly"},
		},
		{
			name: "missing template",
			info: Info{AgentName: "friendly", Alias: "friendly"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConcreteAgentIdentityUsesTemplate(tt.info, tt.agent); got != tt.want {
				t.Fatalf("ConcreteAgentIdentityUsesTemplate() = %v, want %v", got, tt.want)
			}
		})
	}
}
