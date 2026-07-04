package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestT3BridgeStartupEnvelopeModel_PrefersResolvedEnvModel(t *testing.T) {
	tp := TemplateParams{
		Env: map[string]string{
			"GC_PROVIDER": "codex",
			"GC_MODEL":    "gpt-5.4-mini",
		},
	}

	if got := t3BridgeStartupEnvelopeModel(tp.Env["GC_PROVIDER"], tp); got != "gpt-5.4-mini" {
		t.Fatalf("startupEnvelopeModel() = %q, want gpt-5.4-mini", got)
	}
}

func TestT3BridgeStartupEnvelopeModel_UsesCurrentProviderDefaults(t *testing.T) {
	tests := []struct {
		name string
		tp   TemplateParams
		want string
	}{
		{
			name: "codex",
			tp:   TemplateParams{Env: map[string]string{"GC_PROVIDER": "codex"}},
			want: "gpt-5-codex",
		},
		{
			// Wrapped codex alias resolves to the codex family default, not
			// the claude fallback (regression: an exact name match used to
			// stamp claude-opus into a codex session's envelope).
			name: "codex-mini",
			tp:   TemplateParams{Env: map[string]string{"GC_PROVIDER": "codex-mini"}},
			want: "gpt-5-codex",
		},
		{
			name: "claude",
			tp:   TemplateParams{Env: map[string]string{"GC_PROVIDER": "claude"}},
			want: "claude-opus-4-6",
		},
		{
			name: "claude wrapper alias",
			tp:   TemplateParams{Env: map[string]string{"GC_PROVIDER": "claude-max"}},
			want: "claude-opus-4-6",
		},
		{
			// Custom provider with a declared base: the builtin ancestor
			// decides the family even though the name matches nothing.
			name: "custom provider with codex ancestor",
			tp: TemplateParams{
				Env:              map[string]string{"GC_PROVIDER": "speedy"},
				ResolvedProvider: &config.ResolvedProvider{Name: "speedy", BuiltinAncestor: "codex"},
			},
			want: "gpt-5-codex",
		},
		{
			// Fully custom provider with no resolvable family: no model is
			// guessed. requiredThreadModel matches threads by model, so a
			// fabricated default would force thread recreation on resume.
			name: "fully custom provider omits model",
			tp:   TemplateParams{Env: map[string]string{"GC_PROVIDER": "bespoke"}},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := t3BridgeStartupEnvelopeModel(tt.tp.Env["GC_PROVIDER"], tt.tp); got != tt.want {
				t.Fatalf("startupEnvelopeModel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTemplateParamsToConfigInjectsGCAliasFromAlias(t *testing.T) {
	tp := TemplateParams{
		Command:                  "codex",
		TemplateName:             "t3code/witness",
		InstanceName:             "t3code/witness",
		Alias:                    "t3code/witness",
		EffectiveSessionProvider: "t3bridge",
		Env: map[string]string{
			"GC_AGENT":        "t3code/witness",
			"GC_SESSION_NAME": "t3code--witness",
		},
	}

	cfg := templateParamsToConfig(tp)
	if got := cfg.Env["GC_ALIAS"]; got != "t3code/witness" {
		t.Fatalf("GC_ALIAS = %q, want %q", got, "t3code/witness")
	}
}

func TestTemplateParamsToConfigPreservesExistingGCAlias(t *testing.T) {
	tp := TemplateParams{
		Command:                  "codex",
		TemplateName:             "deacon",
		InstanceName:             "deacon",
		Alias:                    "deacon",
		EffectiveSessionProvider: "t3bridge",
		Env: map[string]string{
			"GC_AGENT":        "deacon",
			"GC_SESSION_NAME": "deacon",
			"GC_ALIAS":        "custom-alias",
		},
	}

	cfg := templateParamsToConfig(tp)
	if got := cfg.Env["GC_ALIAS"]; got != "custom-alias" {
		t.Fatalf("GC_ALIAS = %q, want %q", got, "custom-alias")
	}
	if got := tp.Env["GC_ALIAS"]; got != "custom-alias" {
		t.Fatalf("tp.Env[GC_ALIAS] mutated to %q", got)
	}
}

func TestTemplateParamsToConfigDoesNotInjectGCAliasForNonT3Provider(t *testing.T) {
	tp := TemplateParams{
		Command:                  "codex",
		TemplateName:             "deacon",
		InstanceName:             "deacon",
		Alias:                    "deacon",
		EffectiveSessionProvider: "",
		Env: map[string]string{
			"GC_AGENT":        "deacon",
			"GC_SESSION_NAME": "deacon",
		},
	}

	cfg := templateParamsToConfig(tp)
	if got := cfg.Env["GC_ALIAS"]; got != "" {
		t.Fatalf("GC_ALIAS = %q, want empty for non-t3 provider", got)
	}
}

func TestBuildT3BridgeStartupEnvelopeOnlyForT3Provider(t *testing.T) {
	nonT3 := TemplateParams{
		Command:                  "codex",
		TemplateName:             "deacon",
		InstanceName:             "deacon",
		EffectiveSessionProvider: "",
		Env: map[string]string{
			"GC_CITY_PATH": "/data/projects/gc",
			"GC_PROVIDER":  "codex",
		},
	}
	if got := buildT3BridgeStartupEnvelope(nonT3, "prompt"); got != nil {
		t.Fatalf("buildT3BridgeStartupEnvelope(nonT3) = %q, want nil", string(got))
	}

	t3 := nonT3
	t3.EffectiveSessionProvider = "t3bridge"
	if got := buildT3BridgeStartupEnvelope(t3, "prompt"); len(got) == 0 {
		t.Fatal("buildT3BridgeStartupEnvelope(t3) = empty, want envelope")
	}
}
