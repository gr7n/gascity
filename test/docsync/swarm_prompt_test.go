package docsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwarmCoderUsesRoutedWorkClaimProtocol(t *testing.T) {
	root := repoRoot()
	agentDir := filepath.Join(root, "examples", "swarm", "packs", "swarm", "agents", "coder")

	prompt, err := os.ReadFile(filepath.Join(agentDir, "prompt.template.md"))
	if err != nil {
		t.Fatalf("reading swarm coder prompt: %v", err)
	}
	promptText := string(prompt)
	if !strings.Contains(promptText, "gc hook --claim --drain-ack --json") {
		t.Error("swarm coder startup must atomically claim routed work with gc hook")
	}
	if strings.Contains(promptText, "gc bd ready --unassigned") {
		t.Error("swarm coder must not use raw bd ready as its routed-work discovery protocol")
	}

	agent, err := os.ReadFile(filepath.Join(agentDir, "agent.toml"))
	if err != nil {
		t.Fatalf("reading swarm coder agent config: %v", err)
	}
	if !strings.Contains(string(agent), "gc hook --claim --drain-ack --json") {
		t.Error("swarm coder nudge must repeat the routed-work claim protocol")
	}
}
