import { describe, expect, it } from "vitest";

import { formatOperatorAddress, hasBackgroundParticipant, isBackgroundIdentity, isBackgroundRecord, redactBackgroundPayload } from "./background";

describe("background identity helpers", () => {
  it("recognizes legacy mayor identities across common address formats", () => {
    expect(isBackgroundIdentity("mayor")).toBe(true);
    expect(isBackgroundIdentity("Mayor")).toBe(true);
    expect(isBackgroundIdentity("rig/mayor")).toBe(true);
    expect(isBackgroundIdentity("gastown.mayor")).toBe(true);
    expect(isBackgroundIdentity("rig--mayor")).toBe(true);
    expect(isBackgroundIdentity("infra-worker")).toBe(true);
    expect(isBackgroundIdentity("rig/infra-worker")).toBe(true);
    expect(isBackgroundIdentity("sess-k8s-canary")).toBe(true);
    expect(isBackgroundIdentity("reviewer")).toBe(false);
    expect(isBackgroundIdentity("new-mayoralty")).toBe(false);
  });

  it("recognizes explicit background visibility markers", () => {
    expect(isBackgroundRecord({ name: "janitor", visibility: "background" })).toBe(true);
    expect(isBackgroundRecord({ name: "janitor", metadata: { visibility: "internal" } })).toBe(true);
    expect(isBackgroundRecord({ name: "reviewer", operator_visible: false })).toBe(true);
    expect(isBackgroundRecord({ name: "director" })).toBe(false);
  });

  it("redacts operator-facing labels without losing non-background addresses", () => {
    expect(formatOperatorAddress("gastown.mayor")).toBe("Automation");
    expect(formatOperatorAddress("rig/infra-worker")).toBe("Automation");
    expect(formatOperatorAddress("rig/reviewer")).toBe("rig/reviewer");
    expect(hasBackgroundParticipant({ from: "director", to: "mayor" })).toBe(true);
    expect(hasBackgroundParticipant({ from: "director", to: "reviewer" })).toBe(false);
  });

  it("redacts background identities inside raw output payloads", () => {
    expect(redactBackgroundPayload({
      items: [
        { assignee: "mayor", nested: { session_name: "rig/infra-worker" } },
        { assignee: "reviewer" },
      ],
    })).toEqual({
      items: [
        { assignee: "Automation", nested: { session_name: "Automation" } },
        { assignee: "reviewer" },
      ],
    });
  });
});
