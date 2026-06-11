package controlkind

import "testing"

func TestControlKindClassificationsPreserveExistingDistinctions(t *testing.T) {
	if !IsControlDispatcher(Retry) {
		t.Fatal("retry should route to control dispatcher")
	}
	if !IsAttemptControlKind(Retry) {
		t.Fatal("retry should use attempt-control routing")
	}
	if IsScopeCheckExempt(Retry) {
		t.Fatal("retry should not be scope-check exempt")
	}
	for _, kind := range []string{Drain, Tally} {
		if !IsControlDispatcher(kind) {
			t.Fatalf("%s should route to control dispatcher", kind)
		}
		if IsAttemptControlKind(kind) {
			t.Fatalf("%s should not use attempt-control routing", kind)
		}
		if IsLatestAttemptCandidateExempt(kind) {
			t.Fatalf("%s should not be latest-attempt candidate exempt", kind)
		}
	}
	if !IsWorkflowTopology(Workflow) || !IsWorkflowTopology(Scope) || !IsWorkflowTopology(Spec) {
		t.Fatal("workflow, scope, and spec should be workflow topology kinds")
	}
	if IsControlDispatcher(Workflow) || IsControlDispatcher(Scope) || IsControlDispatcher(Spec) {
		t.Fatal("workflow topology kinds should not route to control dispatcher")
	}
	if IsControlDispatcher("task") || RequiresGraphContract("task") {
		t.Fatal("ordinary task kind should not have control metadata")
	}
}

func TestControlCatalogRuntimeRequirements(t *testing.T) {
	fragment := RuntimeRequirements{NeedsCityConfig: true, NeedsFormulaSearchPaths: true, NeedsPrepareFragment: true}
	retry := RuntimeRequirements{NeedsCityConfig: true, NeedsFormulaSearchPaths: true, NeedsSessionRecycle: true}
	tests := map[string]RuntimeRequirements{
		Check:            fragment,
		Fanout:           fragment,
		Ralph:            retry,
		Retry:            retry,
		RetryEval:        {NeedsCityConfig: true, NeedsSessionRecycle: true},
		WorkflowFinalize: {NeedsCityConfig: true, NeedsSourceWorkflowCoordination: true},
	}
	for kind, want := range tests {
		got, ok := RuntimeRequirementsFor(kind)
		if !ok {
			t.Fatalf("RuntimeRequirementsFor(%q) not found", kind)
		}
		if got != want {
			t.Fatalf("RuntimeRequirementsFor(%q) = %+v, want %+v", kind, got, want)
		}
	}
	if _, ok := RuntimeRequirementsFor(Workflow); ok {
		t.Fatal("workflow topology kind unexpectedly has control runtime requirements")
	}
}

func TestControlCatalogRoutingAndFinalizeSemantics(t *testing.T) {
	tests := map[string]GraphRouteMode{
		Check:            GraphRouteMergeDeps,
		Fanout:           GraphRouteControlFor,
		RetryEval:        GraphRouteRetryEvalSubject,
		ScopeCheck:       GraphRouteControlFor,
		WorkflowFinalize: GraphRouteFallback,
	}
	for kind, want := range tests {
		if got := GraphRouteModeFor(kind); got != want {
			t.Fatalf("GraphRouteModeFor(%q) = %q, want %q", kind, got, want)
		}
	}
	for _, kind := range ControlDispatcherKinds() {
		got := SkipsOrphanedWorkflowRootClose(kind)
		want := kind == WorkflowFinalize
		if got != want {
			t.Fatalf("SkipsOrphanedWorkflowRootClose(%q) = %t, want %t", kind, got, want)
		}
	}
}
