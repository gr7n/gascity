package api

import (
	"encoding/json"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

var (
	markdownImageAssetPattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	localImageAssetPattern    = regexp.MustCompile(`(?i)(?:^|[\s"'(])((?:~[/\\]|\.{1,2}[/\\]|/|[A-Za-z0-9_.@%+-]+[/\\])[^\s"'<>()[\]!:]*\.(?:png|jpe?g|gif|webp))(?:$|[\s"'),.;:])`)
)

// entryToTurn converts a provider transcript entry to a human-readable output turn.
func entryToTurn(e *worker.TranscriptEntry) outputTurn {
	turn := outputTurn{
		Role: e.Type,
	}
	if !e.Timestamp.IsZero() {
		turn.Timestamp = e.Timestamp.Format("2006-01-02T15:04:05Z07:00")
	}

	// Try plain string content (message is a JSON object with string content).
	if text := e.TextContent(); text != "" {
		turn.Text = text
		turn.Parts = appendOutputParts(turn.Parts, outputPart{Type: "text", Text: text})
		assets := extractImageAssetsFromText(text, "text")
		turn.Assets = appendOutputAssets(turn.Assets, assets...)
		turn.Parts = appendOutputParts(turn.Parts, outputPartsFromAssets(assets)...)
		return turn
	}

	// Try structured content blocks — extract human-readable text.
	if blocks := e.ContentBlocks(); len(blocks) > 0 {
		var parts []string
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					parts = append(parts, b.Text)
					turn.Parts = appendOutputParts(turn.Parts, outputPart{Type: "text", Text: b.Text})
					assets := extractImageAssetsFromText(b.Text, "text")
					turn.Assets = appendOutputAssets(turn.Assets, assets...)
					turn.Parts = appendOutputParts(turn.Parts, outputPartsFromAssets(assets)...)
				}
			case "tool_use":
				if b.Name != "" {
					parts = append(parts, formatToolUseText(b.Name, b.Input))
				}
				turn.Parts = appendOutputParts(turn.Parts, outputPart{
					Type:  "tool",
					ID:    b.ID,
					Name:  b.Name,
					Tool:  b.Name,
					Input: cloneOutputRawJSON(b.Input),
				})
				assets := extractImageAssetsFromToolInput(b.Input, "tool_use")
				turn.Assets = appendOutputAssets(turn.Assets, assets...)
				turn.Parts = appendOutputParts(turn.Parts, outputPartsFromAssets(assets)...)
			case "tool_result":
				text := extractToolResultText(b.Content)
				turn.Parts = appendOutputParts(turn.Parts, outputPart{
					Type:      "tool",
					ToolUseID: b.ToolUseID,
					Name:      b.Name,
					Tool:      b.Name,
					Output:    cloneOutputRawJSON(b.Content),
					IsError:   b.IsError,
				})
				if text != "" {
					assets := extractImageAssetsFromText(text, "tool_result")
					turn.Assets = appendOutputAssets(turn.Assets, assets...)
					turn.Parts = appendOutputParts(turn.Parts, outputPartsFromAssets(assets)...)
					if len(text) > 500 {
						text = text[:500] + "…"
					}
					parts = append(parts, "[result] "+text)
				}
			case "thinking":
				if text := thinkingBlockText(thinkingBlockInlineText(b), b.Content); text != "" {
					turn.Trace = appendOutputTrace(turn.Trace, outputTrace{Kind: "thinking", Text: text})
					turn.Parts = appendOutputParts(turn.Parts, outputPart{Type: "reasoning", Text: text})
				}
			case "interaction":
				turn.Parts = appendOutputParts(turn.Parts, outputPart{
					Type:      "interaction",
					ID:        b.ID,
					RequestID: b.RequestID,
					Kind:      b.Kind,
					State:     b.State,
					Text:      b.Text,
					Prompt:    b.Prompt,
					Options:   append([]string(nil), b.Options...),
					Action:    b.Action,
				})
			}
		}
		turn.Text = strings.Join(parts, "\n")
		return turn
	}

	// Claude JSONL double-encodes the message field as a JSON string
	// containing JSON. Unwrap and try again.
	turn.Text = unwrapDoubleEncoded(e.Message)
	if turn.Text != "" {
		turn.Parts = appendOutputParts(turn.Parts, outputPart{Type: "text", Text: turn.Text})
	}
	assets := extractImageAssetsFromText(turn.Text, "text")
	turn.Assets = appendOutputAssets(turn.Assets, assets...)
	turn.Parts = appendOutputParts(turn.Parts, outputPartsFromAssets(assets)...)
	return turn
}

func historyEntryToTurn(entry worker.HistoryEntry) outputTurn {
	turn := outputTurn{
		Role: entry.Kind,
	}
	if turn.Role == "" {
		turn.Role = string(entry.Actor)
	}
	if entry.Timestamp != nil {
		turn.Timestamp = entry.Timestamp.Format("2006-01-02T15:04:05Z07:00")
	}

	if len(entry.Blocks) > 0 {
		var parts []string
		for _, block := range entry.Blocks {
			switch block.Kind {
			case worker.BlockKindText:
				if block.Text != "" {
					parts = append(parts, block.Text)
					turn.Parts = appendOutputParts(turn.Parts, outputPart{Type: "text", Text: block.Text})
					assets := extractImageAssetsFromText(block.Text, "text")
					turn.Assets = appendOutputAssets(turn.Assets, assets...)
					turn.Parts = appendOutputParts(turn.Parts, outputPartsFromAssets(assets)...)
				}
			case worker.BlockKindToolUse:
				if block.Name != "" {
					parts = append(parts, formatToolUseText(block.Name, block.Input))
				}
				turn.Parts = appendOutputParts(turn.Parts, outputPart{
					Type:      "tool",
					ID:        block.ToolUseID,
					ToolUseID: block.ToolUseID,
					Name:      block.Name,
					Tool:      block.Name,
					Input:     cloneOutputRawJSON(block.Input),
				})
				assets := extractImageAssetsFromToolInput(block.Input, "tool_use")
				turn.Assets = appendOutputAssets(turn.Assets, assets...)
				turn.Parts = appendOutputParts(turn.Parts, outputPartsFromAssets(assets)...)
			case worker.BlockKindToolResult:
				text := extractToolResultText(block.Content)
				turn.Parts = appendOutputParts(turn.Parts, outputPart{
					Type:      "tool",
					ID:        block.ToolUseID,
					ToolUseID: block.ToolUseID,
					Name:      block.Name,
					Tool:      block.Name,
					Output:    cloneOutputRawJSON(block.Content),
					IsError:   block.IsError,
				})
				if text != "" {
					assets := extractImageAssetsFromText(text, "tool_result")
					turn.Assets = appendOutputAssets(turn.Assets, assets...)
					turn.Parts = appendOutputParts(turn.Parts, outputPartsFromAssets(assets)...)
					if len(text) > 500 {
						text = text[:500] + "…"
					}
					parts = append(parts, "[result] "+text)
				}
			case worker.BlockKindThinking:
				if text := thinkingBlockText(block.Text, block.Content); text != "" {
					turn.Trace = appendOutputTrace(turn.Trace, outputTrace{Kind: "thinking", Text: text})
					turn.Parts = appendOutputParts(turn.Parts, outputPart{Type: "reasoning", Text: text})
				}
			case worker.BlockKindInteraction:
				part := outputPart{
					Type:      "interaction",
					ID:        block.ToolUseID,
					ToolUseID: block.ToolUseID,
					Text:      block.Text,
				}
				if block.Interaction != nil {
					part.RequestID = block.Interaction.RequestID
					part.Kind = block.Interaction.Kind
					part.State = string(block.Interaction.State)
					part.Prompt = block.Interaction.Prompt
					part.Options = append([]string(nil), block.Interaction.Options...)
					part.Action = block.Interaction.Action
				}
				turn.Parts = appendOutputParts(turn.Parts, part)
			}
		}
		turn.Text = strings.Join(parts, "\n")
		if outputTurnHasContent(turn) {
			return turn
		}
	}

	if strings.TrimSpace(entry.Text) != "" {
		turn.Text = entry.Text
		turn.Parts = appendOutputParts(turn.Parts, outputPart{Type: "text", Text: turn.Text})
		assets := extractImageAssetsFromText(turn.Text, "text")
		turn.Assets = appendOutputAssets(turn.Assets, assets...)
		turn.Parts = appendOutputParts(turn.Parts, outputPartsFromAssets(assets)...)
		return turn
	}
	if turn.Text == "" {
		turn.Text = historyRawEntryText(entry.Provenance.Raw)
		if turn.Text != "" {
			turn.Parts = appendOutputParts(turn.Parts, outputPart{Type: "text", Text: turn.Text})
		}
		assets := extractImageAssetsFromText(turn.Text, "text")
		turn.Assets = appendOutputAssets(turn.Assets, assets...)
		turn.Parts = appendOutputParts(turn.Parts, outputPartsFromAssets(assets)...)
	}
	return turn
}

func historySnapshotTurns(snapshot *worker.HistorySnapshot) ([]outputTurn, []string) {
	if snapshot == nil {
		return nil, nil
	}
	turns := make([]outputTurn, 0, len(snapshot.Entries))
	ids := make([]string, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		if !historyEntryVisibleInConversation(entry) {
			continue
		}
		turn := historyEntryToTurn(entry)
		if !outputTurnHasContent(turn) {
			continue
		}
		if len(turns) > 0 && outputTurnsEquivalent(turns[len(turns)-1], turn) {
			continue
		}
		turns = append(turns, turn)
		ids = append(ids, entry.ID)
	}
	return turns, ids
}

func outputTurnHasContent(turn outputTurn) bool {
	return turn.Text != "" || len(turn.Parts) > 0 || len(turn.Assets) > 0 || len(turn.Trace) > 0
}

func appendOutputTurnDistinct(turns []outputTurn, turn outputTurn) []outputTurn {
	if len(turns) > 0 && outputTurnsEquivalent(turns[len(turns)-1], turn) {
		return turns
	}
	return append(turns, turn)
}

func outputTurnsEquivalent(a, b outputTurn) bool {
	a.Timestamp = ""
	b.Timestamp = ""
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && string(left) == string(right)
}

func formatToolUseText(name string, input json.RawMessage) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	label := "[" + name + "]"
	if len(input) == 0 {
		return label
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		raw := strings.TrimSpace(string(input))
		if raw == "" {
			return label
		}
		return label + "\n" + trimToolDetail(raw)
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return label
	}
	return label + "\n" + trimToolDetail(string(pretty))
}

func trimToolDetail(text string) string {
	const maxToolDetailRunes = 4000
	text = strings.TrimSpace(text)
	if len([]rune(text)) <= maxToolDetailRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxToolDetailRunes]) + "…"
}

func sessionTranscriptProvider(info session.Info) string {
	if provider := strings.TrimSpace(info.ProviderKind); provider != "" {
		return provider
	}
	return info.Provider
}

func sessionTranscriptResponseProvider(info session.Info, transcript *worker.TranscriptResult) string {
	if transcript != nil {
		if provider := strings.TrimSpace(transcript.Provider); provider != "" {
			return provider
		}
	}
	return sessionTranscriptProvider(info)
}

func historyEntryVisibleInConversation(entry worker.HistoryEntry) bool {
	switch entry.Provenance.RawType {
	case "user", "assistant", "system", "result":
		return true
	}
	switch entry.Kind {
	case "user", "assistant", "system", "result":
		return true
	default:
		return false
	}
}

func historySnapshotRawMessages(snapshot *worker.HistorySnapshot) ([]json.RawMessage, []string) {
	if snapshot == nil {
		return nil, nil
	}
	rawMessages := make([]json.RawMessage, 0, len(snapshot.Entries))
	ids := make([]string, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		if len(entry.Provenance.Raw) == 0 {
			continue
		}
		rawMessages = append(rawMessages, entry.Provenance.Raw)
		ids = append(ids, entry.ID)
	}
	return rawMessages, ids
}

func historySnapshotActivity(snapshot *worker.HistorySnapshot) string {
	if snapshot == nil {
		return ""
	}
	switch snapshot.TailState.Activity {
	case worker.TailActivityIdle:
		return "idle"
	case worker.TailActivityInTurn:
		return "in-turn"
	default:
		return ""
	}
}

// extractToolResultText extracts human-readable text from a tool_result
// Content field (json.RawMessage). The content can be a plain string or
// an array of content blocks (e.g., [{type:"text", text:"..."}]).
func extractToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try plain string.
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	// Try array of content blocks.
	var blocks []worker.TranscriptContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func thinkingBlockText(text string, raw json.RawMessage) string {
	if text = strings.TrimSpace(text); text != "" {
		return text
	}
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []worker.TranscriptContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, block := range blocks {
			if text := thinkingBlockInlineText(block); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func thinkingBlockInlineText(block worker.TranscriptContentBlock) string {
	if text := strings.TrimSpace(block.Text); text != "" {
		return text
	}
	return strings.TrimSpace(block.Thinking)
}

func appendOutputTrace(existing []outputTrace, incoming ...outputTrace) []outputTrace {
	for _, trace := range incoming {
		trace.Kind = strings.TrimSpace(trace.Kind)
		trace.Text = strings.TrimSpace(trace.Text)
		if trace.Kind == "" || trace.Text == "" {
			continue
		}
		existing = append(existing, trace)
	}
	return existing
}

func outputPartsFromAssets(assets []outputAsset) []outputPart {
	parts := make([]outputPart, 0, len(assets))
	for _, asset := range assets {
		if asset.Kind != "" && asset.Kind != "image" {
			continue
		}
		kind := asset.Kind
		if kind == "" {
			kind = "image"
		}
		parts = append(parts, outputPart{
			Type:   "file",
			Kind:   kind,
			Name:   asset.Name,
			Path:   asset.Path,
			URL:    asset.URL,
			Mime:   "image/*",
			Source: asset.Source,
		})
	}
	return parts
}

func appendOutputParts(existing []outputPart, incoming ...outputPart) []outputPart {
	if len(incoming) == 0 {
		return existing
	}
	seenFiles := make(map[string]bool, len(existing)+len(incoming))
	for _, part := range existing {
		if key := outputPartFileKey(part); key != "" {
			seenFiles[key] = true
		}
	}
	for _, part := range incoming {
		part.Type = strings.TrimSpace(part.Type)
		if part.Type == "" {
			continue
		}
		if key := outputPartFileKey(part); key != "" {
			if seenFiles[key] {
				continue
			}
			seenFiles[key] = true
		}
		existing = append(existing, part)
	}
	return existing
}

func outputPartFileKey(part outputPart) string {
	if part.Type != "file" {
		return ""
	}
	if part.URL != "" {
		return "url:" + part.URL
	}
	if part.Path != "" {
		return "path:" + part.Path
	}
	return ""
}

func cloneOutputRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// unwrapDoubleEncoded handles Claude's double-encoded message format
// where the "message" field is a JSON string containing a JSON object.
// Returns the human-readable content text, or "" if not parseable.
func unwrapDoubleEncoded(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var inner string
	if err := json.Unmarshal(raw, &inner); err == nil {
		raw = []byte(inner)
	}
	var mc worker.TranscriptMessageContent
	if err := json.Unmarshal(raw, &mc); err != nil {
		return ""
	}
	// Try string content.
	var s string
	if err := json.Unmarshal(mc.Content, &s); err == nil && s != "" {
		return s
	}
	// Try array of content blocks.
	var blocks []worker.TranscriptContentBlock
	if err := json.Unmarshal(mc.Content, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func historyRawEntryText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var entry struct {
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return ""
	}
	return unwrapDoubleEncoded(entry.Message)
}

func extractImageAssetsFromText(text, source string) []outputAsset {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var assets []outputAsset
	for _, match := range markdownImageAssetPattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		asset, ok := outputAssetFromReference(match[2], strings.TrimSpace(match[1]), source)
		if ok {
			assets = appendOutputAssets(assets, asset)
		}
	}
	for _, match := range localImageAssetPattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		asset, ok := outputAssetFromReference(match[1], "", source)
		if ok {
			assets = appendOutputAssets(assets, asset)
		}
	}
	return assets
}

func extractImageAssetsFromToolInput(raw json.RawMessage, source string) []outputAsset {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return extractImageAssetsFromText(string(raw), source)
	}
	var refs []string
	collectImageRefsFromJSON(value, &refs)
	var assets []outputAsset
	for _, ref := range refs {
		asset, ok := outputAssetFromReference(ref, "", source)
		if ok {
			assets = appendOutputAssets(assets, asset)
		}
	}
	return assets
}

func collectImageRefsFromJSON(value any, refs *[]string) {
	switch typed := value.(type) {
	case string:
		if looksLikeLocalImageReference(typed) || strings.HasPrefix(typed, "/v0/city/") {
			*refs = append(*refs, typed)
		}
	case []any:
		for _, item := range typed {
			collectImageRefsFromJSON(item, refs)
		}
	case map[string]any:
		for _, item := range typed {
			collectImageRefsFromJSON(item, refs)
		}
	}
}

func outputAssetFromReference(ref, name, source string) (outputAsset, bool) {
	ref = strings.TrimSpace(ref)
	ref = strings.Trim(ref, "`'\"")
	if ref == "" {
		return outputAsset{}, false
	}
	if strings.Contains(ref, ".gc/dashboard/attachments/") || strings.Contains(ref, `.gc\dashboard\attachments\`) {
		return outputAsset{}, false
	}
	lower := strings.ToLower(ref)
	if strings.HasPrefix(lower, "data:") {
		return outputAsset{}, false
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return outputAsset{}, false
	}
	if strings.HasPrefix(ref, "/v0/city/") && strings.Contains(ref, "/session/") && strings.Contains(ref, "/attachments/") {
		return outputAsset{
			Kind:   "image",
			Name:   imageAssetName(ref, name),
			URL:    ref,
			Source: source,
		}, true
	}
	if strings.HasPrefix(lower, "file://") {
		return outputAsset{}, false
	}
	if !looksLikeLocalImageReference(ref) {
		return outputAsset{}, false
	}
	return outputAsset{
		Kind:   "image",
		Name:   imageAssetName(ref, name),
		Path:   ref,
		Source: source,
	}, true
}

func appendOutputAssets(existing []outputAsset, incoming ...outputAsset) []outputAsset {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]bool, len(existing)+len(incoming))
	for _, asset := range existing {
		seen[outputAssetKey(asset)] = true
	}
	for _, asset := range incoming {
		if asset.Kind == "" {
			asset.Kind = "image"
		}
		key := outputAssetKey(asset)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		existing = append(existing, asset)
	}
	return existing
}

func outputAssetKey(asset outputAsset) string {
	if asset.URL != "" {
		return "url:" + asset.URL
	}
	if asset.Path != "" {
		return "path:" + asset.Path
	}
	return ""
}

func looksLikeLocalImageReference(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	refNoQuery := stripAssetQueryFragment(ref)
	ext := strings.ToLower(filepath.Ext(refNoQuery))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func imageAssetName(ref, fallback string) string {
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback
	}
	ref = stripAssetQueryFragment(ref)
	if parsed, err := url.Parse(ref); err == nil && parsed.Path != "" {
		ref = parsed.Path
	}
	if strings.HasPrefix(ref, "/v0/") {
		return path.Base(ref)
	}
	base := filepath.Base(ref)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "image"
	}
	return base
}

func stripAssetQueryFragment(ref string) string {
	if idx := strings.IndexAny(ref, "?#"); idx >= 0 {
		return ref[:idx]
	}
	return ref
}
