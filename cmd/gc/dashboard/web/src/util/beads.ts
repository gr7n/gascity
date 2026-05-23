import type { BeadRecord } from "../api";

type DashboardBeadLike = Pick<BeadRecord, "issue_type" | "labels">;

export function isDashboardInternalBead(bead: DashboardBeadLike): boolean {
  const issueType = (bead.issue_type ?? "").toLowerCase();
  if (issueType === "convoy" || issueType === "session") return true;
  return (bead.labels ?? []).some(isDashboardInternalLabel);
}

function isDashboardInternalLabel(label: string): boolean {
  const normalized = label.toLowerCase();
  return normalized.startsWith("gc:queue") ||
    normalized.startsWith("gc:message") ||
    normalized.startsWith("gc:session");
}
