// Package controlkind centralizes metadata about gc.kind values used by
// graph.v2 compilation, routing, and control dispatch.
package controlkind

import (
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// Known gc.kind values used by the control catalog.
const (
	Check                = beadmeta.KindCheck
	Cleanup              = beadmeta.KindCleanup
	Drain                = beadmeta.KindDrain
	Fanout               = beadmeta.KindFanout
	Ralph                = beadmeta.KindRalph
	Retry                = beadmeta.KindRetry
	RetryEval            = beadmeta.KindRetryEval
	ReviewQuorumFinalize = beadmeta.KindReviewQuorumFinalize
	ReviewQuorumPlan     = beadmeta.KindReviewQuorumPlan
	RetryRun             = beadmeta.KindRetryRun
	Run                  = beadmeta.KindRun
	Scope                = beadmeta.KindScope
	ScopeCheck           = beadmeta.KindScopeCheck
	Spec                 = beadmeta.KindSpec
	Workflow             = beadmeta.KindWorkflow
	WorkflowFinalize     = beadmeta.KindWorkflowFinalize
)

// GraphRouteMode describes how a graph step should route dependency context.
type GraphRouteMode string

// Graph route modes used by control-kind metadata.
const (
	GraphRouteDefault          GraphRouteMode = ""
	GraphRouteControlFor       GraphRouteMode = "control_for"
	GraphRouteFallback         GraphRouteMode = "fallback"
	GraphRouteRetryEvalSubject GraphRouteMode = "retry_eval_subject"
	GraphRouteMergeDeps        GraphRouteMode = "merge_deps"
)

// RuntimeRequirements describes caller-supplied dependencies a control handler
// needs beyond the bead store itself.
type RuntimeRequirements struct {
	NeedsCityConfig                 bool
	NeedsFormulaSearchPaths         bool
	NeedsPrepareFragment            bool
	NeedsSessionRecycle             bool
	NeedsSourceWorkflowCoordination bool
}

// KindSpec captures shared control-kind semantics independent of handlers.
type KindSpec struct {
	Kind                          string
	flags                         flags
	Runtime                       RuntimeRequirements
	GraphRouteMode                GraphRouteMode
	SkipOrphanedWorkflowRootClose bool
}

type flags uint16

const (
	controlDispatcher flags = 1 << iota
	attemptControl
	workflowTopology
	requiresGraphContract
	detachedGraphStep
	scopeCheckExempt
	ralphOutputExempt
	dynamicScopeControl
	latestAttemptCandidateExempt
)

const (
	baseControl          = controlDispatcher | attemptControl | latestAttemptCandidateExempt
	graphControl         = baseControl | requiresGraphContract | dynamicScopeControl
	scopedGraphControl   = graphControl | scopeCheckExempt | ralphOutputExempt
	detachedGraphControl = graphControl | detachedGraphStep
)

var (
	fragmentRuntime = RuntimeRequirements{
		NeedsCityConfig:         true,
		NeedsFormulaSearchPaths: true,
		NeedsPrepareFragment:    true,
	}
	retryRuntime = RuntimeRequirements{
		NeedsCityConfig:         true,
		NeedsFormulaSearchPaths: true,
		NeedsSessionRecycle:     true,
	}
	retryEvalRuntime = RuntimeRequirements{
		NeedsCityConfig:     true,
		NeedsSessionRecycle: true,
	}
	workflowFinalizeRuntime = RuntimeRequirements{
		NeedsCityConfig:                 true,
		NeedsSourceWorkflowCoordination: true,
	}
)

// specs is the built-in control catalog for gc.kind metadata. Handler
// functions stay in their owning packages, but graph/runtime semantics live
// here so formulas, routing, dispatch, and lint-style checks share one source.
var specs = map[string]KindSpec{
	Check:                {flags: scopedGraphControl | detachedGraphStep, Runtime: fragmentRuntime, GraphRouteMode: GraphRouteMergeDeps},
	Cleanup:              {flags: requiresGraphContract},
	Drain:                {flags: baseControl | requiresGraphContract | scopeCheckExempt | ralphOutputExempt},
	Fanout:               {flags: baseControl | dynamicScopeControl | scopeCheckExempt | ralphOutputExempt, Runtime: fragmentRuntime, GraphRouteMode: GraphRouteControlFor},
	Ralph:                {flags: detachedGraphControl | ralphOutputExempt, Runtime: retryRuntime},
	Retry:                {flags: detachedGraphControl, Runtime: retryRuntime},
	RetryEval:            {flags: detachedGraphControl, Runtime: retryEvalRuntime, GraphRouteMode: GraphRouteRetryEvalSubject},
	ReviewQuorumFinalize: {flags: scopedGraphControl},
	ReviewQuorumPlan:     {flags: scopedGraphControl},
	RetryRun:             {flags: requiresGraphContract | detachedGraphStep},
	Run:                  {flags: requiresGraphContract | detachedGraphStep},
	Scope:                {flags: workflowTopology | requiresGraphContract | scopeCheckExempt | ralphOutputExempt},
	ScopeCheck:           {flags: scopedGraphControl, GraphRouteMode: GraphRouteControlFor},
	Spec:                 {flags: workflowTopology | scopeCheckExempt | ralphOutputExempt},
	Workflow:             {flags: workflowTopology | latestAttemptCandidateExempt},
	WorkflowFinalize: {
		flags:                         scopedGraphControl,
		Runtime:                       workflowFinalizeRuntime,
		GraphRouteMode:                GraphRouteFallback,
		SkipOrphanedWorkflowRootClose: true,
	},
}

// Lookup returns the catalog entry for kind after trimming whitespace.
func Lookup(kind string) (KindSpec, bool) {
	key := strings.TrimSpace(kind)
	spec, ok := specs[key]
	spec.Kind = key
	return spec, ok
}

func has(kind string, flag flags) bool {
	spec, ok := Lookup(kind)
	return ok && spec.flags&flag != 0
}

func kindsWith(flag flags) []string {
	kinds := make([]string, 0, len(specs))
	for kind, spec := range specs {
		if spec.flags&flag != 0 {
			kinds = append(kinds, kind)
		}
	}
	sort.Strings(kinds)
	return kinds
}

// IsControlDispatcher reports whether kind is handled by ProcessControl.
func IsControlDispatcher(kind string) bool {
	return has(kind, controlDispatcher)
}

// ControlDispatcherKinds returns every kind handled by ProcessControl.
func ControlDispatcherKinds() []string {
	return kindsWith(controlDispatcher)
}

// IsAttemptControlKind reports whether attempt routing treats kind as control infrastructure.
func IsAttemptControlKind(kind string) bool {
	return has(kind, attemptControl)
}

// RuntimeRequirementsFor returns extra dispatcher dependencies required by kind.
func RuntimeRequirementsFor(kind string) (RuntimeRequirements, bool) {
	spec, ok := Lookup(kind)
	if !ok || spec.flags&controlDispatcher == 0 {
		return RuntimeRequirements{}, false
	}
	return spec.Runtime, true
}

// GraphRouteModeFor returns the graph routing mode associated with kind.
func GraphRouteModeFor(kind string) GraphRouteMode {
	spec, ok := Lookup(kind)
	if !ok {
		return GraphRouteDefault
	}
	return spec.GraphRouteMode
}

// SkipsOrphanedWorkflowRootClose reports whether kind should stay open when its workflow root is absent.
func SkipsOrphanedWorkflowRootClose(kind string) bool {
	spec, ok := Lookup(kind)
	return ok && spec.SkipOrphanedWorkflowRootClose
}

// IsWorkflowTopology reports whether kind represents graph structure rather than runnable work.
func IsWorkflowTopology(kind string) bool {
	return has(kind, workflowTopology)
}

// RequiresGraphContract reports whether kind requires the graph.v2 formula contract.
func RequiresGraphContract(kind string) bool {
	return has(kind, requiresGraphContract)
}

// IsDetachedGraphStep reports whether kind should be detached from ordinary graph step routing.
func IsDetachedGraphStep(kind string) bool {
	return has(kind, detachedGraphStep)
}

// IsScopeCheckExempt reports whether kind should avoid injected scope checks.
func IsScopeCheckExempt(kind string) bool {
	return has(kind, scopeCheckExempt)
}

// IsRalphOutputExempt reports whether kind should avoid Ralph output propagation.
func IsRalphOutputExempt(kind string) bool {
	return has(kind, ralphOutputExempt)
}

// IsDynamicScopeControl reports whether kind participates in dynamic scope control.
func IsDynamicScopeControl(kind string) bool {
	return has(kind, dynamicScopeControl)
}

// IsLatestAttemptCandidateExempt reports whether kind is infrastructure, not an attempt candidate.
func IsLatestAttemptCandidateExempt(kind string) bool {
	return has(kind, latestAttemptCandidateExempt)
}
