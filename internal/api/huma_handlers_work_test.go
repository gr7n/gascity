package api

import (
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestWorkReviewDurablyDeduplicatesAtomicHumanResponse(t *testing.T) {
	store := &workReviewStore{MemStore: beads.NewMemStore()}
	created, err := store.Create(beads.Bead{ID: "gr-decision", Title: "Choose", Type: "decision", Labels: []string{"needs-you"}})
	if err != nil {
		t.Fatal(err)
	}
	state := newFakeState(t)
	state.cityBeadStore = store
	state.stores = map[string]beads.Store{}
	server := &Server{state: state}
	input := workReviewInput(created.ID, "review-request-1", "Approve", "email:bryce@gr7n.com")

	first, err := server.humaHandleWorkReview(context.Background(), input)
	if err != nil {
		t.Fatalf("first review: %v", err)
	}
	second, err := server.humaHandleWorkReview(context.Background(), input)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if first.Body.Status != "closed" || second.Body.Status != "closed" || store.calls != 1 {
		t.Fatalf("first=%+v second=%+v calls=%d", first.Body, second.Body, store.calls)
	}

	conflict := workReviewInput(created.ID, "review-request-2", "Deny", "email:bryce@gr7n.com")
	if _, err := server.humaHandleWorkReview(context.Background(), conflict); err == nil {
		t.Fatal("different review unexpectedly replaced the closed decision")
	}
}

func TestWorkReviewNeverRetriesAfterAmbiguousBackendFailure(t *testing.T) {
	store := &workReviewStore{MemStore: beads.NewMemStore(), failure: errors.New("connection lost")}
	created, err := store.Create(beads.Bead{ID: "gr-ambiguous", Title: "Choose", Type: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	state := newFakeState(t)
	state.cityBeadStore = store
	state.stores = map[string]beads.Store{}
	server := &Server{state: state}
	input := workReviewInput(created.ID, "review-request-3", "Approve", "email:bryce@gr7n.com")
	if _, err := server.humaHandleWorkReview(context.Background(), input); err == nil {
		t.Fatal("ambiguous delivery unexpectedly succeeded")
	}
	if _, err := server.humaHandleWorkReview(context.Background(), input); err == nil {
		t.Fatal("ambiguous retry unexpectedly repeated the delivery")
	}
	if store.calls != 1 {
		t.Fatalf("atomic responder calls = %d, want 1", store.calls)
	}
}

type workReviewStore struct {
	*beads.MemStore
	calls   int
	failure error
}

func (s *workReviewStore) RespondToHuman(id, _, _ string) error {
	s.calls++
	if s.failure != nil {
		return s.failure
	}
	return s.Close(id)
}

func workReviewInput(id, requestID, response, actor string) *WorkReviewInput {
	input := &WorkReviewInput{ID: id, IdempotencyKey: requestID}
	input.Body.RequestID = requestID
	input.Body.Response = response
	input.Body.Actor = actor
	return input
}
