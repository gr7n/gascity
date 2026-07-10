package config

import (
	"strings"
	"testing"
)

func TestNamedSessionOperatorVisibilityDefaultsToOperator(t *testing.T) {
	ns := NamedSession{Template: "worker"}
	if got := ns.OperatorVisibilityOrDefault(); got != NamedSessionOperatorVisibilityOperator {
		t.Fatalf("OperatorVisibilityOrDefault() = %q, want operator", got)
	}
	if !ns.OperatorVisible() {
		t.Fatal("OperatorVisible() = false, want true")
	}
	if !ns.ChatVisible() {
		t.Fatal("ChatVisible() = false, want true")
	}
}

func TestNamedSessionOperatorVisibilityBackgroundHidesChat(t *testing.T) {
	ns := NamedSession{Template: "worker", OperatorVisibility: " BACKGROUND "}
	if got := ns.OperatorVisibilityOrDefault(); got != NamedSessionOperatorVisibilityBackground {
		t.Fatalf("OperatorVisibilityOrDefault() = %q, want background", got)
	}
	if ns.OperatorVisible() {
		t.Fatal("OperatorVisible() = true, want false")
	}
	if ns.ChatVisible() {
		t.Fatal("ChatVisible() = true, want false")
	}
}

func TestValidateNamedSessionsRejectsInvalidOperatorVisibility(t *testing.T) {
	cfg := &City{
		Workspace: Workspace{Name: "test-city"},
		Agents: []Agent{{
			Name: "worker",
		}},
		NamedSessions: []NamedSession{{
			Template:           "worker",
			OperatorVisibility: "spotlight",
		}},
	}

	_, err := ValidateNamedSessions(cfg)
	if err == nil {
		t.Fatal("ValidateNamedSessions() = nil, want invalid operator_visibility error")
	}
	if !strings.Contains(err.Error(), "operator_visibility") {
		t.Fatalf("ValidateNamedSessions() error = %v, want operator_visibility", err)
	}
}
