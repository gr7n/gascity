const BACKGROUND_VISIBILITY = new Set(["background", "hidden", "internal", "system"]);

export function isBackgroundRecord(value: unknown): boolean {
  const record = asRecord(value);
  const metadata = asRecord(record.metadata);
  return booleanValue(record.background) ||
    record.operator_visible === false ||
    record.chat_visible === false ||
    isBackgroundVisibility(record.visibility) ||
    isBackgroundVisibility(record.ui_visibility) ||
    isBackgroundVisibility(record.audience) ||
    isBackgroundVisibility(record.display) ||
    isBackgroundVisibility(metadata.visibility) ||
    isBackgroundIdentity(record.alias) ||
    isBackgroundIdentity(record.assignee) ||
    isBackgroundIdentity(record.actor) ||
    isBackgroundIdentity(record.display_name) ||
    isBackgroundIdentity(record.from) ||
    isBackgroundIdentity(record.id) ||
    isBackgroundIdentity(record.name) ||
    isBackgroundIdentity(record.session_name) ||
    isBackgroundIdentity(record.subject) ||
    isBackgroundIdentity(record.template) ||
    isBackgroundIdentity(record.to);
}

export function hasBackgroundParticipant(record: { from?: unknown; to?: unknown }): boolean {
  return isBackgroundIdentity(record.from) || isBackgroundIdentity(record.to);
}

export function isBackgroundIdentity(value: unknown): boolean {
  const identity = stringValue(value);
  if (!identity) return false;
  if (identity === "mayor") return true;
  const normalized = identity.replace(/--/g, "/");
  const segments = normalized.split(/[/.]/).filter(Boolean);
  return segments[segments.length - 1] === "mayor";
}

export function formatOperatorAddress(value: string | undefined | null): string | undefined {
  if (!value) return value ?? undefined;
  return isBackgroundIdentity(value) ? "Internal" : value;
}

function asRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  return value as Record<string, unknown>;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value.trim().toLowerCase() : "";
}

function booleanValue(value: unknown): boolean {
  if (value === true) return true;
  return ["true", "yes", "1"].includes(stringValue(value));
}

function isBackgroundVisibility(value: unknown): boolean {
  return BACKGROUND_VISIBILITY.has(stringValue(value));
}
