package session

// PromptReceipt records the provenance of the rendered template projection
// associated with a session start. Version is the human-readable template
// frontmatter value; SHA identifies template substitution plus configured
// template fragments. Runtime delivery envelopes added after rendering are
// intentionally excluded, so this is a drift-grouping receipt rather than a
// hash of the full provider input envelope.
//
// A nil *PromptReceipt means the caller has no receipt observation. A non-nil
// zero value is an observed start with no rendered template prompt and clears
// any stale receipt from an earlier start.
type PromptReceipt struct {
	Version string
	SHA     string
}

const (
	promptVersionMetadataKey = "prompt_version"
	promptSHAMetadataKey     = "prompt_sha"
)

// PromptReceiptFromMetadata decodes the persisted prompt receipt. Legacy
// sessions have the zero value; callers must preserve that absence rather than
// synthesizing a receipt from the current template.
func PromptReceiptFromMetadata(metadata map[string]string) PromptReceipt {
	return PromptReceipt{
		Version: metadata[promptVersionMetadataKey],
		SHA:     metadata[promptSHAMetadataKey],
	}
}

// PromptReceiptPatch returns the session-owned metadata patch for an observed
// prompt receipt. Nil means no observation and produces no writes. A non-nil
// receipt always writes both keys so a later prompt-less start clears stale
// provenance atomically.
func PromptReceiptPatch(receipt *PromptReceipt) MetadataPatch {
	if receipt == nil {
		return nil
	}
	return MetadataPatch{
		promptVersionMetadataKey: receipt.Version,
		promptSHAMetadataKey:     receipt.SHA,
	}
}

// WithPromptReceiptMetadata returns metadata with the observed receipt
// applied through the session-owned metadata vocabulary.
func WithPromptReceiptMetadata(metadata map[string]string, receipt *PromptReceipt) map[string]string {
	return PromptReceiptPatch(receipt).Apply(metadata)
}

// RecordPromptReceipt persists the rendered prompt provenance for a session.
// It is the command boundary used by startup hooks that render the prompt in
// the provider process rather than in the controller's launch preparation.
func (s *Store) RecordPromptReceipt(id string, receipt PromptReceipt) error {
	return s.ApplyPatch(id, PromptReceiptPatch(&receipt))
}
