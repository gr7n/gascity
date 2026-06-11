package dispatch

import (
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/controlkind"
)

type controlHandler func(beads.Store, beads.Bead, ProcessOptions) (ControlResult, error)

var controlHandlers = map[string]controlHandler{
	controlkind.Retry:                processRetryControl,
	controlkind.Ralph:                processRalphControl,
	controlkind.Check:                processRalphCheck,
	controlkind.RetryEval:            processRetryEval,
	controlkind.ReviewQuorumFinalize: processReviewQuorumFinalize,
	controlkind.ReviewQuorumPlan:     processReviewQuorumPlan,
	controlkind.Fanout:               processFanout,
	controlkind.Drain:                processDrain,
	controlkind.ScopeCheck:           processScopeCheck,
	controlkind.WorkflowFinalize:     processWorkflowFinalize,
}

func controlHandlerFor(kind string) (controlHandler, bool) {
	handler, ok := controlHandlers[strings.TrimSpace(kind)]
	return handler, ok
}

// RegisteredControlKinds returns the gc.kind values handled by ProcessControl.
func RegisteredControlKinds() []string {
	kinds := make([]string, 0, len(controlHandlers))
	for kind := range controlHandlers {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}
