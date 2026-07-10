package api

import (
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	workdirutil "github.com/gastownhall/gascity/internal/workdir"
)

type agentCreateContext struct {
	Agent        config.Agent
	Alias        string
	ExplicitName string
	Identity     string
	WorkDir      string
}

// ensureAgentCreateMessageAccepted gates the optional create-time message at
// the same effective capability boundary as submit/message/nudge. Creating a
// command-worker session without a message remains valid; accepting and then
// silently dropping an initial_message is not.
func ensureAgentCreateMessageAccepted(agent *config.Agent, message string) error {
	if strings.TrimSpace(message) == "" {
		return nil
	}
	return config.EnsureAgentAcceptsPrompt(agent)
}

func (s *Server) resolveAgentCreateContext(template, alias string) (agentCreateContext, error) {
	cfg := s.state.Config()
	if cfg == nil {
		return agentCreateContext{}, fmt.Errorf("no city config loaded")
	}
	agentCfg, ok := resolveSessionTemplateAgent(cfg, template)
	if !ok {
		return agentCreateContext{}, fmt.Errorf("resolved agent template disappeared: %s", template)
	}
	if alias != "" && agentCfg.SupportsMultipleSessions() {
		alias = workdirutil.SessionQualifiedName(s.state.CityPath(), agentCfg, cfg.Rigs, alias, "")
	}
	explicitName, err := sessionExplicitNameForCreate(agentCfg, alias)
	if err != nil {
		return agentCreateContext{}, err
	}
	identity := workdirutil.SessionQualifiedName(s.state.CityPath(), agentCfg, cfg.Rigs, alias, explicitName)
	workDir, err := s.resolveSessionWorkDir(agentCfg, identity)
	if err != nil {
		return agentCreateContext{}, err
	}
	return agentCreateContext{
		Agent:        agentCfg,
		Alias:        strings.TrimSpace(alias),
		ExplicitName: explicitName,
		Identity:     identity,
		WorkDir:      workDir,
	}, nil
}

func alwaysNamedSessionCreateConflict(cfg *config.City, target string) (string, bool) {
	named := config.FindNamedSession(cfg, strings.TrimSpace(target))
	if named == nil || named.ModeOrDefault() != "always" {
		return "", false
	}
	identity := named.QualifiedName()
	if identity == "" {
		identity = strings.TrimSpace(target)
	}
	return fmt.Sprintf(
		"agent %q is an always-on named session; use POST /v0/session/%s/messages or /v0/session/%s/submit instead of POST /v0/sessions",
		strings.TrimSpace(target),
		identity,
		identity,
	), true
}
