package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

func TestEntryToTurnAddsImageAssetsFromTextAndToolResults(t *testing.T) {
	toolText := strings.Repeat("x", 620) + " screenshots/final.png"
	message := transcriptMessage(t, []worker.TranscriptContentBlock{
		{Type: "text", Text: "See ![chart](charts/chart.png) and /v0/city/test-city/session/s-1/attachments/abc123abc123abc123abc123abc123ab/photo.png"},
		{Type: "tool_result", Content: mustRawJSON(t, toolText)},
		{Type: "thinking", Thinking: "secret-plan.png"},
	})
	turn := entryToTurn(&worker.TranscriptEntry{Type: "assistant", Message: message})

	if !strings.Contains(turn.Text, "[result] ") {
		t.Fatalf("turn.Text = %q, want tool result summary", turn.Text)
	}
	if strings.Contains(turn.Text, "screenshots/final.png") {
		t.Fatalf("turn.Text = %q, should stay truncated before late image path", turn.Text)
	}
	if strings.Contains(turn.Text, "secret-plan.png") {
		t.Fatalf("turn.Text included thinking text inline: %q", turn.Text)
	}
	if len(turn.Trace) != 1 || turn.Trace[0].Kind != "thinking" || turn.Trace[0].Text != "secret-plan.png" {
		t.Fatalf("Trace = %#v, want collapsed thinking trace", turn.Trace)
	}
	if len(turn.Parts) != 6 {
		t.Fatalf("Parts = %#v, want text, files, tool, file, reasoning", turn.Parts)
	}
	if turn.Parts[0].Type != "text" || !strings.Contains(turn.Parts[0].Text, "See ![chart]") {
		t.Fatalf("Parts[0] = %#v, want text part", turn.Parts[0])
	}
	if turn.Parts[3].Type != "tool" || len(turn.Parts[3].Output) == 0 {
		t.Fatalf("Parts[3] = %#v, want tool result part", turn.Parts[3])
	}
	if turn.Parts[5].Type != "reasoning" || turn.Parts[5].Text != "secret-plan.png" {
		t.Fatalf("Parts[5] = %#v, want reasoning part", turn.Parts[5])
	}
	if len(turn.Assets) != 3 {
		t.Fatalf("Assets = %#v, want 3 image assets", turn.Assets)
	}
	assertAssetPath(t, turn.Assets, "charts/chart.png", "text")
	assertAssetURL(t, turn.Assets, "/v0/city/test-city/session/s-1/attachments/abc123abc123abc123abc123abc123ab/photo.png", "text")
	assertAssetPath(t, turn.Assets, "screenshots/final.png", "tool_result")
}

func TestEntryToTurnDeduplicatesRepeatedImageReferences(t *testing.T) {
	message := transcriptMessage(t, []worker.TranscriptContentBlock{{
		Type: "text",
		Text: "![one](screen.png)\nAgain: screen.png\n![two](screen.png)",
	}})
	turn := entryToTurn(&worker.TranscriptEntry{Type: "assistant", Message: message})

	if len(turn.Assets) != 1 {
		t.Fatalf("Assets = %#v, want one deduplicated asset", turn.Assets)
	}
	if turn.Assets[0].Path != "screen.png" {
		t.Fatalf("Path = %q, want screen.png", turn.Assets[0].Path)
	}
}

func TestEntryToTurnAddsImageAssetFromToolUseInput(t *testing.T) {
	message := transcriptMessage(t, []worker.TranscriptContentBlock{{
		Type:  "tool_use",
		Name:  "view_image",
		Input: mustRawJSONObject(t, map[string]any{"path": "../shots/preview.png"}),
	}})
	turn := entryToTurn(&worker.TranscriptEntry{Type: "assistant", Message: message})

	if !strings.Contains(turn.Text, "[view_image]") || !strings.Contains(turn.Text, "../shots/preview.png") {
		t.Fatalf("Text = %q, want tool call summary with input detail", turn.Text)
	}
	if len(turn.Parts) != 2 || turn.Parts[0].Type != "tool" || turn.Parts[1].Type != "file" {
		t.Fatalf("Parts = %#v, want tool plus file parts", turn.Parts)
	}
	assertAssetPath(t, turn.Assets, "../shots/preview.png", "tool_use")
}

func TestAppendOutputTurnDistinctDeduplicatesConsecutiveTraceOnlyTurns(t *testing.T) {
	turns := []outputTurn{}
	turns = appendOutputTurnDistinct(turns, outputTurn{
		Role:  "assistant",
		Trace: []outputTrace{{Kind: "thinking", Text: "same reasoning"}},
	})
	turns = appendOutputTurnDistinct(turns, outputTurn{
		Role:      "assistant",
		Timestamp: "2026-05-28T00:00:00Z",
		Trace:     []outputTrace{{Kind: "thinking", Text: "same reasoning"}},
	})
	turns = appendOutputTurnDistinct(turns, outputTurn{
		Role:  "assistant",
		Trace: []outputTrace{{Kind: "thinking", Text: "different reasoning"}},
	})

	if len(turns) != 2 {
		t.Fatalf("turns = %#v, want duplicate trace-only turn collapsed", turns)
	}
}

func TestEntryToTurnIgnoresBareImageNamesInProse(t *testing.T) {
	message := transcriptMessage(t, []worker.TranscriptContentBlock{{
		Type: "text",
		Text: "Saw second image: preview.png.",
	}})
	turn := entryToTurn(&worker.TranscriptEntry{Type: "assistant", Message: message})

	if len(turn.Assets) != 0 {
		t.Fatalf("Assets = %#v, want no assets for a bare filename mention", turn.Assets)
	}
}

func TestEntryToTurnRejectsFileURLImageReferences(t *testing.T) {
	message := transcriptMessage(t, []worker.TranscriptContentBlock{{
		Type: "text",
		Text: "Look at file:///tmp/preview.png",
	}})
	turn := entryToTurn(&worker.TranscriptEntry{Type: "assistant", Message: message})

	if len(turn.Assets) != 0 {
		t.Fatalf("Assets = %#v, want no file:// asset references", turn.Assets)
	}
}

func TestSessionTranscriptProviderPrefersProviderKind(t *testing.T) {
	got := sessionTranscriptProvider(session.Info{
		Provider:     "gr7n-router",
		ProviderKind: "codex",
	})
	if got != "codex" {
		t.Fatalf("sessionTranscriptProvider() = %q, want codex", got)
	}
}

func transcriptMessage(t *testing.T, blocks []worker.TranscriptContentBlock) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(struct {
		Role    string                          `json:"role"`
		Content []worker.TranscriptContentBlock `json:"content"`
	}{
		Role:    "assistant",
		Content: blocks,
	})
	if err != nil {
		t.Fatalf("marshal transcript message: %v", err)
	}
	return payload
}

func mustRawJSON(t *testing.T, value string) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal raw JSON: %v", err)
	}
	return payload
}

func mustRawJSONObject(t *testing.T, value map[string]any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal raw JSON object: %v", err)
	}
	return payload
}

func assertAssetPath(t *testing.T, assets []outputAsset, path, source string) {
	t.Helper()
	for _, asset := range assets {
		if asset.Path == path && asset.Source == source && asset.Kind == "image" {
			return
		}
	}
	t.Fatalf("Assets = %#v, missing path %q source %q", assets, path, source)
}

func assertAssetURL(t *testing.T, assets []outputAsset, assetURL, source string) {
	t.Helper()
	for _, asset := range assets {
		if asset.URL == assetURL && asset.Source == source && asset.Kind == "image" {
			return
		}
	}
	t.Fatalf("Assets = %#v, missing url %q source %q", assets, assetURL, source)
}
