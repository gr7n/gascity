package session

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestPromptReceiptMetadataRoundTrip(t *testing.T) {
	receipt := &PromptReceipt{Version: "v3", SHA: "abc123"}
	metadata := WithPromptReceiptMetadata(map[string]string{"state": "active"}, receipt)

	got := PromptReceiptFromMetadata(metadata)
	if got != *receipt {
		t.Fatalf("PromptReceiptFromMetadata() = %+v, want %+v", got, *receipt)
	}
	if metadata["state"] != "active" {
		t.Fatalf("unrelated metadata was lost: %v", metadata)
	}
}

func TestPromptReceiptNilPreservesLegacyAbsence(t *testing.T) {
	metadata := WithPromptReceiptMetadata(map[string]string{"state": "active"}, nil)
	if _, ok := metadata[promptVersionMetadataKey]; ok {
		t.Fatalf("legacy metadata gained %q: %v", promptVersionMetadataKey, metadata)
	}
	if _, ok := metadata[promptSHAMetadataKey]; ok {
		t.Fatalf("legacy metadata gained %q: %v", promptSHAMetadataKey, metadata)
	}
	if got := PromptReceiptFromMetadata(metadata); got != (PromptReceipt{}) {
		t.Fatalf("legacy receipt = %+v, want zero", got)
	}
}

func TestRecordPromptReceiptClearsStaleReceiptAtomically(t *testing.T) {
	store := beads.NewMemStore()
	created, err := store.Create(beads.Bead{
		Type:   BeadType,
		Labels: []string{LabelSession},
		Metadata: WithPromptReceiptMetadata(map[string]string{
			"state": "active",
		}, &PromptReceipt{Version: "v1", SHA: "old"}),
	})
	if err != nil {
		t.Fatal(err)
	}

	front := NewStore(beads.SessionStore{Store: store})
	if err := front.RecordPromptReceipt(created.ID, PromptReceipt{}); err != nil {
		t.Fatalf("RecordPromptReceipt: %v", err)
	}
	updated, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := PromptReceiptFromMetadata(updated.Metadata); got != (PromptReceipt{}) {
		t.Fatalf("cleared receipt = %+v, want zero", got)
	}
}
