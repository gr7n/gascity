package dispatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/reviewquorum"
)

const (
	reviewQuorumLanesSchema   = "review-quorum.lanes.v1"
	reviewQuorumLaneSchema    = "review-quorum.lane.v1"
	reviewQuorumSummarySchema = "review-quorum.summary.v1"
)

type reviewQuorumLanePlan struct {
	Lanes []reviewQuorumLaneSpec `json:"lanes"`
}

type reviewQuorumLaneSpec struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Target   string `json:"target"`
	Focus    string `json:"focus,omitempty"`
}

func processReviewQuorumPlan(store beads.Store, bead beads.Bead, _ ProcessOptions) (ControlResult, error) {
	raw := strings.TrimSpace(bead.Metadata[beadmeta.ReviewQuorumLanesJSONMetadataKey])
	if raw == "" {
		return ControlResult{}, fmt.Errorf("%w: %s: missing gc.review_quorum_lanes_json", ErrControlGraphMalformed, bead.ID)
	}
	plan, err := parseReviewQuorumLanePlan(raw)
	if err != nil {
		return ControlResult{}, fmt.Errorf("%w: %s: parsing lanes: %w", ErrControlGraphMalformed, bead.ID, err)
	}
	if err := validateReviewQuorumLanePlan(plan); err != nil {
		return ControlResult{}, fmt.Errorf("%w: %s: validating lanes: %w", ErrControlGraphMalformed, bead.ID, err)
	}
	plan = normalizeReviewQuorumLanePlan(plan)
	output, err := json.Marshal(plan)
	if err != nil {
		return ControlResult{}, fmt.Errorf("%s: marshaling lane plan: %w", bead.ID, err)
	}
	closeMetadata := map[string]string{
		beadmeta.OutcomeMetadataKey:          "pass",
		beadmeta.OutputJSONMetadataKey:       string(output),
		beadmeta.OutputJSONSchemaMetadataKey: reviewQuorumLanesSchema,
	}
	clearControllerSpawnErrorMetadata(closeMetadata)
	if err := updateMetadataAndClose(store, bead.ID, closeMetadata); err != nil {
		return ControlResult{}, fmt.Errorf("%s: closing review quorum lane plan: %w", bead.ID, err)
	}
	return ControlResult{Processed: true, Action: "review-quorum-plan"}, nil
}

func processReviewQuorumFinalize(store beads.Store, bead beads.Bead, _ ProcessOptions) (ControlResult, error) {
	subject := strings.TrimSpace(bead.Metadata[beadmeta.ReviewQuorumSubjectMetadataKey])
	baseRef := strings.TrimSpace(bead.Metadata[beadmeta.ReviewQuorumBaseRefMetadataKey])

	blockers, err := reviewQuorumClosedBlockers(store, bead.ID)
	if err != nil {
		if errors.Is(err, errFinalizePending) {
			return ControlResult{}, ErrControlPending
		}
		return ControlResult{}, fmt.Errorf("%s: resolving review quorum blockers: %w", bead.ID, err)
	}

	var expected []string
	var laneBeads []beads.Bead
	if sourceRef := strings.TrimSpace(bead.Metadata[beadmeta.ReviewQuorumLanesSourceMetadataKey]); sourceRef != "" {
		rootID := strings.TrimSpace(bead.Metadata[beadmeta.RootBeadIDMetadataKey])
		if rootID == "" {
			return ControlResult{}, fmt.Errorf("%s: missing gc.root_bead_id for dynamic review quorum finalizer", bead.ID)
		}
		all, err := listByWorkflowRoot(store, rootID)
		if err != nil {
			return ControlResult{}, fmt.Errorf("%s: listing workflow beads: %w", bead.ID, err)
		}
		expected, err = expectedReviewQuorumLaneIDs(all, rootID, sourceRef)
		if err != nil {
			return ControlResult{}, fmt.Errorf("%w: %s: loading expected lanes: %w", ErrControlGraphMalformed, bead.ID, err)
		}
		laneBeads, err = collectReviewQuorumLaneBeads(all, expected)
		if err != nil {
			if errors.Is(err, errFinalizePending) {
				return ControlResult{}, ErrControlPending
			}
			return ControlResult{}, fmt.Errorf("%w: %s: collecting lane beads: %w", ErrControlGraphMalformed, bead.ID, err)
		}
	} else {
		laneBeads = collectReviewQuorumLaneBlockers(blockers)
	}
	if len(expected) > 0 {
		if err := ensureExpectedReviewQuorumLanes(laneBeads, expected); err != nil {
			if errors.Is(err, errFinalizePending) {
				return ControlResult{}, ErrControlPending
			}
			return ControlResult{}, fmt.Errorf("%w: %s: %w", ErrControlGraphMalformed, bead.ID, err)
		}
	}

	outputs := make([]reviewquorum.LaneOutput, 0, len(laneBeads))
	for _, lane := range laneBeads {
		outputs = append(outputs, reviewQuorumLaneOutputFromBead(lane))
	}
	summary := reviewquorum.Finalize(subject, baseRef, outputs)
	rawSummary, err := json.Marshal(summary)
	if err != nil {
		return ControlResult{}, fmt.Errorf("%s: marshaling review quorum summary: %w", bead.ID, err)
	}

	closeMetadata := map[string]string{
		beadmeta.OutcomeMetadataKey:             "pass",
		beadmeta.OutputJSONMetadataKey:          string(rawSummary),
		beadmeta.OutputJSONSchemaMetadataKey:    reviewQuorumSummarySchema,
		beadmeta.ReviewQuorumVerdictMetadataKey: summary.Verdict,
	}
	clearControllerSpawnErrorMetadata(closeMetadata)
	if err := updateMetadataAndClose(store, bead.ID, closeMetadata); err != nil {
		return ControlResult{}, fmt.Errorf("%s: closing review quorum finalizer: %w", bead.ID, err)
	}
	return ControlResult{Processed: true, Action: "review-quorum-finalize"}, nil
}

func parseReviewQuorumLanePlan(raw string) (reviewQuorumLanePlan, error) {
	var plan reviewQuorumLanePlan
	if strings.HasPrefix(strings.TrimSpace(raw), "[") {
		if err := json.Unmarshal([]byte(raw), &plan.Lanes); err != nil {
			return plan, err
		}
		return plan, nil
	}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return plan, err
	}
	return plan, nil
}

func validateReviewQuorumLanePlan(plan reviewQuorumLanePlan) error {
	if len(plan.Lanes) == 0 {
		return fmt.Errorf("at least one lane is required")
	}
	configs := make([]reviewquorum.LaneConfig, 0, len(plan.Lanes))
	for _, lane := range plan.Lanes {
		provider := strings.TrimSpace(lane.Provider)
		model := strings.TrimSpace(lane.Model)
		target := strings.TrimSpace(lane.Target)
		if provider == "" {
			return fmt.Errorf("lane %q provider is required", strings.TrimSpace(lane.ID))
		}
		if model == "" {
			return fmt.Errorf("lane %q model is required", strings.TrimSpace(lane.ID))
		}
		if target == "" {
			return fmt.Errorf("lane %q target is required", strings.TrimSpace(lane.ID))
		}
		configs = append(configs, reviewquorum.LaneConfig{
			ID:       lane.ID,
			Provider: provider,
			Model:    model,
		})
	}
	return reviewquorum.ValidateLaneConfigs(configs)
}

func normalizeReviewQuorumLanePlan(plan reviewQuorumLanePlan) reviewQuorumLanePlan {
	normalized := reviewQuorumLanePlan{Lanes: make([]reviewQuorumLaneSpec, len(plan.Lanes))}
	for i, lane := range plan.Lanes {
		normalized.Lanes[i] = reviewQuorumLaneSpec{
			ID:       strings.TrimSpace(lane.ID),
			Provider: strings.TrimSpace(lane.Provider),
			Model:    strings.TrimSpace(lane.Model),
			Target:   strings.TrimSpace(lane.Target),
			Focus:    strings.TrimSpace(lane.Focus),
		}
	}
	return normalized
}

func reviewQuorumClosedBlockers(store beads.Store, beadID string) ([]beads.Bead, error) {
	deps, err := store.DepList(beadID, "down")
	if err != nil {
		return nil, err
	}
	blockers := make([]beads.Bead, 0, len(deps))
	for _, dep := range deps {
		if dep.Type != "blocks" {
			continue
		}
		blocker, err := store.Get(dep.DependsOnID)
		if err != nil {
			return nil, err
		}
		if blocker.Status != "closed" {
			return nil, fmt.Errorf("%w: blocker %s is still open", errFinalizePending, blocker.ID)
		}
		blockers = append(blockers, blocker)
	}
	return blockers, nil
}

func collectReviewQuorumLaneBlockers(blockers []beads.Bead) []beads.Bead {
	lanes := make([]beads.Bead, 0, len(blockers))
	for _, blocker := range blockers {
		if isReviewQuorumLaneBead(blocker) {
			lanes = append(lanes, blocker)
		}
	}
	sortReviewQuorumLaneBeads(lanes)
	return lanes
}

func expectedReviewQuorumLaneIDs(all []beads.Bead, rootID, sourceRef string) ([]string, error) {
	source, err := resolveWorkflowStepByRefFromBeads(all, rootID, sourceRef, workflowStepMatchOptions{})
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(source.Metadata[beadmeta.OutputJSONMetadataKey])
	if raw == "" {
		return nil, fmt.Errorf("source step %s is missing gc.output_json", source.ID)
	}
	plan, err := parseReviewQuorumLanePlan(raw)
	if err != nil {
		return nil, err
	}
	if err := validateReviewQuorumLanePlan(plan); err != nil {
		return nil, err
	}
	plan = normalizeReviewQuorumLanePlan(plan)
	ids := make([]string, 0, len(plan.Lanes))
	for _, lane := range plan.Lanes {
		ids = append(ids, lane.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

func collectReviewQuorumLaneBeads(all []beads.Bead, expected []string) ([]beads.Bead, error) {
	expectedSet := map[string]struct{}{}
	for _, id := range expected {
		expectedSet[id] = struct{}{}
	}
	byLane := map[string]beads.Bead{}
	for _, bead := range all {
		if !isReviewQuorumLaneBead(bead) {
			continue
		}
		laneID := strings.TrimSpace(bead.Metadata[beadmeta.ReviewQuorumLaneMetadataKey])
		if len(expectedSet) > 0 {
			if _, ok := expectedSet[laneID]; !ok {
				continue
			}
		}
		if bead.Status != "closed" {
			return nil, fmt.Errorf("%w: lane %s is still open", errFinalizePending, bead.ID)
		}
		if existing, ok := byLane[laneID]; ok && existing.ID != bead.ID {
			return nil, fmt.Errorf("duplicate lane bead for %q (%s, %s)", laneID, existing.ID, bead.ID)
		}
		byLane[laneID] = bead
	}
	lanes := make([]beads.Bead, 0, len(byLane))
	for _, bead := range byLane {
		lanes = append(lanes, bead)
	}
	sortReviewQuorumLaneBeads(lanes)
	return lanes, nil
}

func isReviewQuorumLaneBead(bead beads.Bead) bool {
	if strings.TrimSpace(bead.Metadata[beadmeta.ReviewQuorumLaneMetadataKey]) == "" {
		return false
	}
	if strings.TrimSpace(bead.Metadata[beadmeta.AttemptMetadataKey]) != "" {
		return false
	}
	return strings.TrimSpace(bead.Metadata[beadmeta.OutputJSONSchemaMetadataKey]) == reviewQuorumLaneSchema ||
		strings.TrimSpace(bead.Metadata[beadmeta.KindMetadataKey]) == "retry"
}

func sortReviewQuorumLaneBeads(lanes []beads.Bead) {
	sort.SliceStable(lanes, func(i, j int) bool {
		return strings.TrimSpace(lanes[i].Metadata[beadmeta.ReviewQuorumLaneMetadataKey]) <
			strings.TrimSpace(lanes[j].Metadata[beadmeta.ReviewQuorumLaneMetadataKey])
	})
}

func ensureExpectedReviewQuorumLanes(laneBeads []beads.Bead, expected []string) error {
	present := map[string]struct{}{}
	for _, bead := range laneBeads {
		present[strings.TrimSpace(bead.Metadata[beadmeta.ReviewQuorumLaneMetadataKey])] = struct{}{}
	}
	var missing []string
	for _, id := range expected {
		if _, ok := present[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing expected lane bead(s): %s", errFinalizePending, strings.Join(missing, ","))
	}
	return nil
}

func reviewQuorumLaneOutputFromBead(bead beads.Bead) reviewquorum.LaneOutput {
	raw := strings.TrimSpace(bead.Metadata[beadmeta.OutputJSONMetadataKey])
	if raw != "" {
		var output reviewquorum.LaneOutput
		if err := json.Unmarshal([]byte(raw), &output); err == nil {
			return output
		}
		return syntheticReviewQuorumLaneOutput(bead, reviewquorum.FailureClassHard, "invalid_output_json")
	}

	class := strings.TrimSpace(bead.Metadata[beadmeta.FailureClassMetadataKey])
	reason := strings.TrimSpace(bead.Metadata[beadmeta.FailureReasonMetadataKey])
	if class == "" {
		class = reviewquorum.FailureClassHard
	}
	if reason == "" {
		reason = "missing_output_json"
	}
	return syntheticReviewQuorumLaneOutput(bead, class, reason)
}

func syntheticReviewQuorumLaneOutput(bead beads.Bead, failureClass, failureReason string) reviewquorum.LaneOutput {
	return reviewquorum.LaneOutput{
		LaneID:        strings.TrimSpace(bead.Metadata[beadmeta.ReviewQuorumLaneMetadataKey]),
		Provider:      strings.TrimSpace(bead.Metadata[beadmeta.ProviderMetadataKey]),
		Model:         strings.TrimSpace(bead.Metadata[beadmeta.ModelMetadataKey]),
		Verdict:       reviewquorum.VerdictBlocked,
		Summary:       strings.TrimSpace(bead.Metadata[beadmeta.FinalDispositionMetadataKey]),
		FindingsCount: 0,
		Findings:      []reviewquorum.Finding{},
		Evidence:      []reviewquorum.Evidence{},
		FailureClass:  failureClass,
		FailureReason: failureReason,
	}
}
