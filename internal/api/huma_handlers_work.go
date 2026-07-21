package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/api/apierr"
	"github.com/gastownhall/gascity/internal/beads"
)

const workReviewIntentMetadataKey = "gr7n.review_intent.v1"

type WorkReviewInput struct {
	CityScope
	ID             string `path:"id" doc:"Durable Work Bead ID."`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" doc:"Stable request ID for durable review reconciliation."`
	Body           struct {
		RequestID string `json:"request_id" minLength:"8" maxLength:"128" doc:"Stable caller request ID."`
		Response  string `json:"response" minLength:"1" maxLength:"12000" doc:"Reviewed human response."`
		Actor     string `json:"actor" minLength:"1" maxLength:"320" doc:"Audited human actor."`
	}
}

// humaHandleWorkReview is deliberately narrower than the generic Bead update
// API: it accepts only an operator response to a Bead explicitly marked as a
// human decision, durably fences the request, then invokes bd's atomic
// comment-and-close operation. A crash after the fence never causes a retry to
// repeat an ambiguous human response.
func (s *Server) humaHandleWorkReview(_ context.Context, input *WorkReviewInput) (*IndexOutput[beads.Bead], error) {
	id := strings.TrimSpace(input.ID)
	requestID := strings.TrimSpace(input.Body.RequestID)
	response := strings.TrimSpace(input.Body.Response)
	actor := strings.TrimSpace(input.Body.Actor)
	if id == "" || requestID == "" || response == "" || actor == "" || input.IdempotencyKey != requestID {
		return nil, apierr.InvalidRequest.Msg("id, request_id, response, and actor are required; Idempotency-Key must equal request_id")
	}
	if len(requestID) > 128 || len(response) > 12000 || len(actor) > 320 {
		return nil, apierr.InvalidRequest.Msg("work review request exceeds its bounded contract")
	}

	for _, store := range s.beadStoresForID(id) {
		current, err := store.Get(id)
		if errors.Is(err, beads.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, apierr.Internal.Msg(err.Error())
		}
		if !isHumanDecision(current) {
			return nil, apierr.ConflictWrongState.Msg("Work Bead is not awaiting human review")
		}

		intent := reviewIntent(requestID, response, actor)
		persisted := strings.TrimSpace(current.Metadata[workReviewIntentMetadataKey])
		if current.Status == "closed" {
			if persisted == intent {
				return workReviewOutput(s, store, current), nil
			}
			return nil, apierr.ConflictWrongState.Msg("Work review is already closed by a different request")
		}
		if persisted != "" {
			if persisted == intent {
				return nil, apierr.OperationInProgress.Msg("matching Work review has an ambiguous prior delivery; manual reconciliation is required")
			}
			return nil, apierr.IdempotencyMismatch.Msg("Work review request_id already names a different response")
		}

		writer, ok := beads.ConditionalWriterFor(store)
		if !ok {
			return nil, apierr.NotImplemented.Msg("Work review requires conditional metadata writes")
		}
		swapped, err := writer.CompareAndSetMetadataKey(id, workReviewIntentMetadataKey, "", intent)
		if err != nil {
			return nil, workReviewMutationError(err)
		}
		if !swapped {
			return nil, apierr.OperationInProgress.Msg("another Work review request won the durable fence")
		}

		responder, ok := beads.HumanResponderFor(store)
		if !ok {
			return nil, apierr.NotImplemented.Msg("Work review backend does not support atomic human responses")
		}
		if err := responder.RespondToHuman(id, response, actor); err != nil {
			return nil, apierr.OperationInProgress.Msg("Work review delivery is ambiguous after its durable fence; manual reconciliation is required")
		}
		closed, err := store.Get(id)
		if err != nil {
			return nil, apierr.OperationInProgress.Msg("Work review committed but its closed state is not yet readable")
		}
		if closed.Status != "closed" {
			return nil, apierr.OperationInProgress.Msg("Work review backend did not expose a closed decision; manual reconciliation is required")
		}
		return workReviewOutput(s, store, closed), nil
	}
	return nil, apierr.BeadNotFound.Msg("bead " + id + " not found")
}

func isHumanDecision(bead beads.Bead) bool {
	if strings.EqualFold(bead.Type, "decision") {
		return true
	}
	for _, label := range bead.Labels {
		switch strings.ToLower(strings.TrimSpace(label)) {
		case "needs-you", "needs/operator", "human-decision", "human-review":
			return true
		}
	}
	return false
}

func reviewIntent(requestID, response, actor string) string {
	hash := sha256.Sum256([]byte(requestID + "\x00" + response + "\x00" + actor))
	return requestID + ":" + hex.EncodeToString(hash[:])
}

func workReviewOutput(s *Server, store beads.Store, bead beads.Bead) *IndexOutput[beads.Bead] {
	return &IndexOutput[beads.Bead]{Index: s.latestIndex(), CacheAgeS: cacheAgeSeconds(store), Body: bead}
}

func workReviewMutationError(err error) error {
	switch {
	case errors.Is(err, beads.ErrNotFound):
		return apierr.BeadNotFound.Msg(err.Error())
	case beads.IsPreconditionFailed(err):
		return apierr.ConflictConcurrentModify.Msg(err.Error())
	case errors.Is(err, beads.ErrConditionalWriteUnsupported):
		return apierr.NotImplemented.Msg(err.Error())
	default:
		return apierr.Internal.Msg(fmt.Sprintf("fencing Work review: %v", err))
	}
}
