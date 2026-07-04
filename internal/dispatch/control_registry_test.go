package dispatch

import (
	"slices"
	"testing"

	"github.com/gastownhall/gascity/internal/controlkind"
)

func TestControlRegistryMatchesDispatcherClassification(t *testing.T) {
	got := RegisteredControlKinds()
	want := controlkind.ControlDispatcherKinds()
	if !slices.Equal(got, want) {
		t.Fatalf("registered control kinds = %v, want dispatcher classifications %v", got, want)
	}
}
