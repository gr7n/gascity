package tmux

import (
	"errors"
	"testing"
	"time"

	runtimepkg "github.com/gastownhall/gascity/internal/runtime"
)

func TestInspectCodexDeliverySegmentAcceptsExactTurn(t *testing.T) {
	segment := []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"user_message","message":"ship the fix"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}` + "\n",
	)

	got := inspectCodexDeliverySegment(segment, "ship the fix")
	if !got.Accepted {
		t.Fatal("Accepted = false, want true")
	}
	if got.ProviderError != nil {
		t.Fatalf("ProviderError = %v, want nil", got.ProviderError)
	}
}

func TestInspectCodexDeliverySegmentReportsImmediateProviderRateLimit(t *testing.T) {
	segment := []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"user_message","message":"ship the fix"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":"codex","limit_name":"five-hour","primary":{"resets_at":1784203200}}}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","error":{"message":"exceeded retry limit, last status: 429 Too Many Requests","retry_after_seconds":17,"codex_error_info":{"response_too_many_failed_attempts":{"http_status_code":429}}}}}` + "\n",
	)

	got := inspectCodexDeliverySegment(segment, "ship the fix")
	if !got.Accepted {
		t.Fatal("Accepted = false, want true")
	}
	if !errors.Is(got.ProviderError, runtimepkg.ErrProviderUnavailable) {
		t.Fatalf("ProviderError = %v, want ErrProviderUnavailable", got.ProviderError)
	}
	var unavailable *runtimepkg.ProviderUnavailableError
	if !errors.As(got.ProviderError, &unavailable) {
		t.Fatalf("ProviderError type = %T, want *ProviderUnavailableError", got.ProviderError)
	}
	if unavailable.StatusCode != 429 {
		t.Fatalf("StatusCode = %d, want 429", unavailable.StatusCode)
	}
	if unavailable.RetryAfter != "17s" {
		t.Fatalf("RetryAfter = %q, want 17s", unavailable.RetryAfter)
	}
	if unavailable.LimitID != "codex" || unavailable.LimitName != "five-hour" {
		t.Fatalf("limit = %q/%q, want codex/five-hour", unavailable.LimitID, unavailable.LimitName)
	}
	if unavailable.ResetsAt != 1784203200 {
		t.Fatalf("ResetsAt = %d, want 1784203200", unavailable.ResetsAt)
	}
}

func TestInspectCodexDeliverySegmentDoesNotCallGenericTurnErrorCapacity(t *testing.T) {
	segment := []byte(
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"user_message","message":"ship the fix"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","error":{"message":"tool execution failed"}}}` + "\n",
	)

	got := inspectCodexDeliverySegment(segment, "ship the fix")
	if !got.Accepted {
		t.Fatal("Accepted = false, want true")
	}
	if got.ProviderError != nil {
		t.Fatalf("ProviderError = %v, want nil for non-capacity task error", got.ProviderError)
	}
}

func TestInspectCodexDeliverySegmentPreservesAmbiguity(t *testing.T) {
	tests := []struct {
		name    string
		segment []byte
	}{
		{
			name: "no task started",
			segment: []byte(
				`{"type":"event_msg","payload":{"type":"user_message","message":"ship the fix"}}` + "\n",
			),
		},
		{
			name: "different message",
			segment: []byte(
				`{"type":"event_msg","payload":{"type":"task_started"}}` + "\n" +
					`{"type":"event_msg","payload":{"type":"user_message","message":"something else"}}` + "\n",
			),
		},
		{
			name: "malformed tail",
			segment: []byte(
				`{"type":"event_msg","payload":{"type":"task_started"}}` + "\n" +
					`{"type":"event_msg","payload":`,
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inspectCodexDeliverySegment(tt.segment, "ship the fix")
			if got.Accepted || got.ProviderError != nil {
				t.Fatalf("got = %#v, want ambiguous observation", got)
			}
		})
	}
}

func TestSubmitCodexEnterUsesTranscriptReceiptWithoutResend(t *testing.T) {
	var enters int
	err := submitCodexEnterAndConfirm(
		func() error { enters++; return nil },
		func() {},
		func() (bool, error) { return false, nil },
		func() (codexDeliveryObservation, error) {
			return codexDeliveryObservation{Accepted: true}, nil
		},
		func(time.Duration) {},
	)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if enters != 1 {
		t.Fatalf("enters = %d, want exactly 1", enters)
	}
}

func TestSubmitCodexEnterSurfacesFastProviderFailureWithoutResend(t *testing.T) {
	var enters int
	providerErr := &runtimepkg.ProviderUnavailableError{StatusCode: 429, RetryAfter: "17s"}
	err := submitCodexEnterAndConfirm(
		func() error { enters++; return nil },
		func() {},
		func() (bool, error) { return false, nil },
		func() (codexDeliveryObservation, error) {
			return codexDeliveryObservation{Accepted: true, ProviderError: providerErr}, nil
		},
		func(time.Duration) {},
	)
	if !errors.Is(err, runtimepkg.ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if enters != 1 {
		t.Fatalf("enters = %d, want exactly 1", enters)
	}
}

func TestSubmitCodexEnterPreservesUnconfirmedAmbiguityWithoutResend(t *testing.T) {
	var enters int
	err := submitCodexEnterAndConfirm(
		func() error { enters++; return nil },
		func() {},
		func() (bool, error) { return false, nil },
		func() (codexDeliveryObservation, error) {
			return codexDeliveryObservation{}, errors.New("rollout unavailable")
		},
		func(time.Duration) {},
	)
	if !errors.Is(err, runtimepkg.ErrDeliveryUnconfirmed) {
		t.Fatalf("err = %v, want ErrDeliveryUnconfirmed", err)
	}
	if enters != 1 {
		t.Fatalf("enters = %d, want exactly 1", enters)
	}
}
