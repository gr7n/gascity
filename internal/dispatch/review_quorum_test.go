package dispatch

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/formula"
	"github.com/gastownhall/gascity/internal/formulatest"
	"github.com/gastownhall/gascity/internal/reviewquorum"
)

func TestProcessReviewQuorumFinalizeSuccessfulTwoLaneSummary(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()
	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	primary := mustCreateReviewQuorumLane(t, store, root.ID, "primary", reviewquorum.LaneOutput{
		LaneID:        "primary",
		Provider:      "provider-a",
		Model:         "model-a",
		Verdict:       reviewquorum.VerdictPassWithFindings,
		Summary:       "found one issue",
		FindingsCount: 1,
		Findings: []reviewquorum.Finding{
			{Severity: "major", Title: "bug", File: "main.go", Start: 12},
		},
		ReadOnlyEnforcement: passingReadOnlyProof(),
		FailureClass:        reviewquorum.FailureClassNone,
	})
	secondary := mustCreateReviewQuorumLane(t, store, root.ID, "secondary", reviewquorum.LaneOutput{
		LaneID:              "secondary",
		Provider:            "provider-b",
		Model:               "model-b",
		Verdict:             reviewquorum.VerdictPass,
		Summary:             "no issues",
		FindingsCount:       0,
		ReadOnlyEnforcement: passingReadOnlyProof(),
		FailureClass:        reviewquorum.FailureClassNone,
	})
	finalizer := mustCreateReviewQuorumFinalizer(t, store, root.ID, "")
	mustDep(t, store, finalizer.ID, primary.ID, "blocks")
	mustDep(t, store, finalizer.ID, secondary.ID, "blocks")

	result, err := ProcessControl(store, finalizer, ProcessOptions{})
	if err != nil {
		t.Fatalf("ProcessControl(review-quorum-finalize): %v", err)
	}
	if !result.Processed || result.Action != "review-quorum-finalize" {
		t.Fatalf("result = %+v, want processed review-quorum-finalize", result)
	}

	closed := mustGet(t, store, finalizer.ID)
	if closed.Status != "closed" || closed.Metadata["gc.outcome"] != "pass" {
		t.Fatalf("finalizer status/outcome = %q/%q, want closed/pass", closed.Status, closed.Metadata["gc.outcome"])
	}
	summary := mustReviewQuorumSummary(t, closed.Metadata["gc.output_json"])
	if summary.Verdict != reviewquorum.VerdictPassWithFindings {
		t.Fatalf("summary verdict = %q, want pass_with_findings", summary.Verdict)
	}
	if summary.FindingsCount != 1 || len(summary.Findings) != 1 {
		t.Fatalf("summary findings = %d/%d, want one finding", summary.FindingsCount, len(summary.Findings))
	}
	if closed.Metadata["gc.review_quorum_verdict"] != reviewquorum.VerdictPassWithFindings {
		t.Fatalf("gc.review_quorum_verdict = %q, want pass_with_findings", closed.Metadata["gc.review_quorum_verdict"])
	}
}

func TestProcessReviewQuorumFinalizeMalformedLaneJSONProducesCanonicalFailedSummary(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()
	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	lane := mustCreate(t, store, beads.Bead{
		Title: "primary lane",
		Metadata: map[string]string{
			"gc.kind":               "retry",
			"gc.root_bead_id":       root.ID,
			"gc.review_quorum_lane": "primary",
			"gc.provider":           "provider-a",
			"gc.model":              "model-a",
			"gc.output_json_schema": "review-quorum.lane.v1",
			"gc.output_json":        "{not-json",
		},
	})
	mustClose(t, store, lane.ID)
	finalizer := mustCreateReviewQuorumFinalizer(t, store, root.ID, "")
	mustDep(t, store, finalizer.ID, lane.ID, "blocks")

	if _, err := ProcessControl(store, finalizer, ProcessOptions{}); err != nil {
		t.Fatalf("ProcessControl(review-quorum-finalize): %v", err)
	}
	closed := mustGet(t, store, finalizer.ID)
	summary := mustReviewQuorumSummary(t, closed.Metadata["gc.output_json"])
	if closed.Metadata["gc.outcome"] != "pass" {
		t.Fatalf("finalizer gc.outcome = %q, want pass for canonical summary write", closed.Metadata["gc.outcome"])
	}
	if summary.Verdict != reviewquorum.VerdictFail {
		t.Fatalf("summary verdict = %q, want fail", summary.Verdict)
	}
	if summary.FailureClass != reviewquorum.FailureClassHard {
		t.Fatalf("summary failure_class = %q, want hard", summary.FailureClass)
	}
	if !strings.Contains(summary.FailureReason, "invalid_output_json") {
		t.Fatalf("summary failure_reason = %q, want invalid_output_json", summary.FailureReason)
	}
}

func TestProcessReviewQuorumFinalizeSoftFailedTransientLaneProducesBlockedSummary(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()
	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	lane := mustCreate(t, store, beads.Bead{
		Title: "secondary lane",
		Metadata: map[string]string{
			"gc.kind":               "retry",
			"gc.root_bead_id":       root.ID,
			"gc.review_quorum_lane": "secondary",
			"gc.provider":           "provider-b",
			"gc.model":              "model-b",
			"gc.output_json_schema": "review-quorum.lane.v1",
			"gc.outcome":            "pass",
			"gc.failure_class":      reviewquorum.FailureClassTransient,
			"gc.failure_reason":     "provider_rate_limited",
			"gc.final_disposition":  "soft_fail",
		},
	})
	mustClose(t, store, lane.ID)
	finalizer := mustCreateReviewQuorumFinalizer(t, store, root.ID, "")
	mustDep(t, store, finalizer.ID, lane.ID, "blocks")

	if _, err := ProcessControl(store, finalizer, ProcessOptions{}); err != nil {
		t.Fatalf("ProcessControl(review-quorum-finalize): %v", err)
	}
	closed := mustGet(t, store, finalizer.ID)
	summary := mustReviewQuorumSummary(t, closed.Metadata["gc.output_json"])
	if summary.Verdict != reviewquorum.VerdictBlocked {
		t.Fatalf("summary verdict = %q, want blocked", summary.Verdict)
	}
	if summary.FailureClass != reviewquorum.FailureClassTransient {
		t.Fatalf("summary failure_class = %q, want transient", summary.FailureClass)
	}
	if summary.FailureReason != "lane=secondary reason=provider_rate_limited" {
		t.Fatalf("summary failure_reason = %q, want lane=secondary reason=provider_rate_limited", summary.FailureReason)
	}
}

func TestProcessReviewQuorumPlanValidatesLaneConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		lanesJSON string
		want      string
	}{
		{name: "zero lanes", lanesJSON: `[]`, want: "at least one lane is required"},
		{name: "duplicate lane id", lanesJSON: `[{"id":"primary","provider":"p","model":"m","target":"a"},{"id":"primary","provider":"p","model":"m","target":"b"}]`, want: "duplicated"},
		{name: "missing lane id", lanesJSON: `[{"provider":"p","model":"m","target":"a"}]`, want: "lane id is required"},
		{name: "missing target", lanesJSON: `[{"id":"primary","provider":"p","model":"m"}]`, want: "target is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := beads.NewMemStore()
			planner := mustCreate(t, store, beads.Bead{
				Title: "planner",
				Metadata: map[string]string{
					"gc.kind":                     "review-quorum-plan",
					"gc.review_quorum_lanes_json": tt.lanesJSON,
				},
			})

			_, err := ProcessControl(store, planner, ProcessOptions{})
			if err == nil {
				t.Fatal("ProcessControl(review-quorum-plan) succeeded, want error")
			}
			if !errors.Is(err, ErrControlGraphMalformed) {
				t.Fatalf("error = %v, want ErrControlGraphMalformed", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestProcessReviewQuorumDynamicFinalizerWaitsForExpectedLanes(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()
	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	planner := mustCreate(t, store, beads.Bead{
		Title: "planner",
		Metadata: map[string]string{
			"gc.root_bead_id":       root.ID,
			"gc.step_ref":           "mol-review-quorum-dynamic.plan-review-lanes",
			"gc.output_json_schema": "review-quorum.lanes.v1",
			"gc.output_json":        `{"lanes":[{"id":"primary","provider":"provider-a","model":"model-a","target":"reviewer-a"},{"id":"secondary","provider":"provider-b","model":"model-b","target":"reviewer-b"}]}`,
		},
	})
	mustClose(t, store, planner.ID)
	mustCreateReviewQuorumLane(t, store, root.ID, "primary", reviewquorum.LaneOutput{
		LaneID:              "primary",
		Provider:            "provider-a",
		Model:               "model-a",
		Verdict:             reviewquorum.VerdictPass,
		FindingsCount:       0,
		ReadOnlyEnforcement: passingReadOnlyProof(),
		FailureClass:        reviewquorum.FailureClassNone,
	})
	finalizer := mustCreateReviewQuorumFinalizer(t, store, root.ID, "plan-review-lanes")
	mustDep(t, store, finalizer.ID, planner.ID, "blocks")

	_, err := ProcessControl(store, finalizer, ProcessOptions{})
	if !errors.Is(err, ErrControlPending) {
		t.Fatalf("ProcessControl with one lane = %v, want ErrControlPending", err)
	}

	mustCreateReviewQuorumLane(t, store, root.ID, "secondary", reviewquorum.LaneOutput{
		LaneID:              "secondary",
		Provider:            "provider-b",
		Model:               "model-b",
		Verdict:             reviewquorum.VerdictPass,
		FindingsCount:       0,
		ReadOnlyEnforcement: passingReadOnlyProof(),
		FailureClass:        reviewquorum.FailureClassNone,
	})

	result, err := ProcessControl(store, mustGet(t, store, finalizer.ID), ProcessOptions{})
	if err != nil {
		t.Fatalf("ProcessControl after second lane: %v", err)
	}
	if !result.Processed || result.Action != "review-quorum-finalize" {
		t.Fatalf("result = %+v, want processed review-quorum-finalize", result)
	}
	summary := mustReviewQuorumSummary(t, mustGet(t, store, finalizer.ID).Metadata["gc.output_json"])
	if summary.Verdict != reviewquorum.VerdictPass {
		t.Fatalf("summary verdict = %q, want pass", summary.Verdict)
	}
	if len(summary.Lanes) != 2 {
		t.Fatalf("summary lanes len = %d, want 2", len(summary.Lanes))
	}
}

func TestProcessReviewQuorumDynamicFanoutSpawnsLanePerConfigItem(t *testing.T) {
	formulatest.EnableV2ForTest(t)

	store := beads.NewMemStore()
	root := mustCreate(t, store, beads.Bead{
		Title: "workflow",
		Metadata: map[string]string{
			"gc.kind":             "workflow",
			"gc.formula_contract": "graph.v2",
		},
	})
	planner := mustCreate(t, store, beads.Bead{
		Title: "planner",
		Metadata: map[string]string{
			"gc.kind":                     "review-quorum-plan",
			"gc.root_bead_id":             root.ID,
			"gc.step_ref":                 "mol-review-quorum-dynamic.plan-review-lanes",
			"gc.review_quorum_lanes_json": `[{"id":"primary","provider":"opencode","model":"kimi-k2.6","target":"reviewer-a","focus":"general review"},{"id":"secondary","provider":"opencode","model":"deepseek-v4-pro","target":"reviewer-b","focus":"regression review"}]`,
		},
	})
	if _, err := ProcessControl(store, planner, ProcessOptions{}); err != nil {
		t.Fatalf("ProcessControl(review-quorum-plan): %v", err)
	}
	planner = mustGet(t, store, planner.ID)
	if planner.Status != "closed" {
		t.Fatalf("planner status = %q, want closed", planner.Status)
	}
	inlineTemplate, err := json.Marshal([]*formula.Step{
		{
			ID:          "{target}.review-{item.id}",
			Title:       "Review lane {item.id}",
			Description: "Review {{subject}} lane {item.id}",
			Metadata: map[string]string{
				"gc.run_target":           "{item.target}",
				"gc.provider":             "{item.provider}",
				"gc.model":                "{item.model}",
				"gc.review_quorum_lane":   "{item.id}",
				"gc.output_json_schema":   "review-quorum.lane.v1",
				"gc.output_json_required": "true",
			},
			Retry: &formula.RetrySpec{MaxAttempts: 3, OnExhausted: "soft_fail"},
		},
	})
	if err != nil {
		t.Fatalf("marshal inline template: %v", err)
	}
	fanout := mustCreate(t, store, beads.Bead{
		Title: "fanout",
		Metadata: map[string]string{
			"gc.kind":            "fanout",
			"gc.root_bead_id":    root.ID,
			"gc.step_ref":        "mol-review-quorum-dynamic.plan-review-lanes-fanout",
			"gc.control_for":     "mol-review-quorum-dynamic.plan-review-lanes",
			"gc.for_each":        "output.lanes",
			"gc.fanout_template": string(inlineTemplate),
			"gc.fanout_mode":     "parallel",
		},
	})
	mustDep(t, store, fanout.ID, planner.ID, "blocks")

	result, err := ProcessControl(store, fanout, ProcessOptions{})
	if err != nil {
		t.Fatalf("ProcessControl(fanout): %v", err)
	}
	if !result.Processed || result.Action != "fanout-spawn" {
		t.Fatalf("result = %+v, want processed fanout-spawn", result)
	}

	all, err := store.List(beads.ListQuery{Metadata: map[string]string{"gc.root_bead_id": root.ID}, IncludeClosed: true})
	if err != nil {
		t.Fatalf("list workflow beads: %v", err)
	}
	lanes := map[string]beads.Bead{}
	for _, bead := range all {
		if bead.Metadata["gc.kind"] != "retry" || bead.Metadata["gc.attempt"] != "" {
			continue
		}
		if lane := bead.Metadata["gc.review_quorum_lane"]; lane != "" {
			lanes[lane] = bead
		}
	}
	if len(lanes) != 2 {
		t.Fatalf("spawned lane controls = %d (%v), want 2", len(lanes), lanes)
	}
	if got := lanes["primary"].Metadata["gc.run_target"]; got != "reviewer-a" {
		t.Fatalf("primary gc.run_target = %q, want reviewer-a", got)
	}
	if got := lanes["secondary"].Metadata["gc.run_target"]; got != "reviewer-b" {
		t.Fatalf("secondary gc.run_target = %q, want reviewer-b", got)
	}
}

func mustCreateReviewQuorumFinalizer(t *testing.T, store beads.Store, rootID, lanesSource string) beads.Bead {
	t.Helper()
	metadata := map[string]string{
		"gc.kind":                   "review-quorum-finalize",
		"gc.root_bead_id":           rootID,
		"gc.review_quorum_subject":  "PR-123",
		"gc.review_quorum_base_ref": "origin/main",
		"gc.output_json_schema":     "review-quorum.summary.v1",
		"gc.output_json_required":   "true",
	}
	if lanesSource != "" {
		metadata["gc.review_quorum_lanes_source"] = lanesSource
	}
	return mustCreate(t, store, beads.Bead{Title: "finalizer", Metadata: metadata})
}

func mustCreateReviewQuorumLane(t *testing.T, store beads.Store, rootID, laneID string, output reviewquorum.LaneOutput) beads.Bead {
	t.Helper()
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal lane output: %v", err)
	}
	lane := mustCreate(t, store, beads.Bead{
		Title: laneID + " lane",
		Metadata: map[string]string{
			"gc.kind":               "retry",
			"gc.root_bead_id":       rootID,
			"gc.review_quorum_lane": laneID,
			"gc.provider":           output.Provider,
			"gc.model":              output.Model,
			"gc.output_json_schema": "review-quorum.lane.v1",
			"gc.output_json":        string(raw),
		},
	})
	mustClose(t, store, lane.ID)
	return lane
}

func mustReviewQuorumSummary(t *testing.T, raw string) reviewquorum.Summary {
	t.Helper()
	if raw == "" {
		t.Fatal("missing gc.output_json")
	}
	var summary reviewquorum.Summary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	return summary
}

func passingReadOnlyProof() reviewquorum.ReadOnlyEnforcement {
	return reviewquorum.ReadOnlyEnforcement{
		Observed:        true,
		Enabled:         true,
		Passed:          true,
		BaselineCommand: "git status --porcelain=v1 -z",
		AfterCommand:    "git status --porcelain=v1 -z",
	}
}
