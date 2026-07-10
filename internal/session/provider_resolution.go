package session

import "strings"

// SessionKindMetadataKey is the persisted discriminator for direct provider
// versus configured agent sessions. It is projected onto Info.SessionKind so
// decision paths do not crack raw bead metadata.
const SessionKindMetadataKey = "real_world_app_session_kind"

// UseAgentTemplateForProviderResolution reports whether a session should
// resolve provider options through its agent template instead of treating the
// persisted Template field as a raw provider name. The provider-name arguments
// are accepted for call-site symmetry but do not disqualify non-manual legacy
// sessions when the agent template still exists.
func UseAgentTemplateForProviderResolution(sessionKind string, metadata map[string]string, _, _ string, templateFound bool) bool {
	sessionKind = strings.TrimSpace(sessionKind)
	switch sessionKind {
	case "provider":
		return false
	case "agent":
		return true
	}
	if metadata == nil {
		return true
	}
	if strings.TrimSpace(metadata["agent_name"]) != "" ||
		strings.TrimSpace(metadata[NamedSessionMetadataKey]) == "true" {
		return true
	}
	if strings.TrimSpace(metadata["session_origin"]) == "manual" {
		return false
	}
	return templateFound
}
