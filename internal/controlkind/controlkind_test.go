package controlkind

import (
	"slices"
	"sort"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

func TestControlKindClassificationsPreserveExistingDistinctions(t *testing.T) {
	assertSameMembers(t, ControlDispatcherKinds(), beadmeta.ControlKinds)

	for _, kind := range beadmeta.ControlKinds {
		if !IsControlDispatcher(kind) {
			t.Fatalf("%s should route to control dispatcher", kind)
		}
		if !IsAttemptControlKind(kind) {
			t.Fatalf("%s should use attempt-control routing", kind)
		}
		if !IsLatestAttemptCandidateExempt(kind) {
			t.Fatalf("%s should be latest-attempt candidate exempt", kind)
		}
	}

	for _, kind := range beadmeta.ScopeCheckExemptKinds {
		if !IsScopeCheckExempt(kind) {
			t.Fatalf("%s should be scope-check exempt", kind)
		}
		if !IsRalphOutputExempt(kind) {
			t.Fatalf("%s should be ralph-output exempt", kind)
		}
	}
	if IsScopeCheckExempt(Retry) || IsScopeCheckExempt(Ralph) || IsScopeCheckExempt(RetryEval) {
		t.Fatal("retry, ralph, and retry-eval should not be scope-check exempt")
	}
	if !IsRalphOutputExempt(Ralph) {
		t.Fatal("ralph should be ralph-output exempt")
	}
	if !IsWorkflowTopology(Workflow) || !IsWorkflowTopology(Scope) || !IsWorkflowTopology(Spec) {
		t.Fatal("workflow, scope, and spec should be workflow topology kinds")
	}
	if IsControlDispatcher(Workflow) || IsControlDispatcher(Scope) || IsControlDispatcher(Spec) {
		t.Fatal("workflow topology kinds should not route to control dispatcher")
	}
	if IsControlDispatcher(beadmeta.KindTask) || RequiresGraphContract(beadmeta.KindTask) {
		t.Fatal("ordinary task kind should not have control metadata")
	}
}

func assertSameMembers(t *testing.T, got, want []string) {
	t.Helper()

	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("members = %v, want %v", got, want)
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
