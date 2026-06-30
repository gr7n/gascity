import { describe, expect, it } from "vitest";

import { formatOperatorAddress, hasBackgroundParticipant, isBackgroundIdentity, isBackgroundRecord } from "./background";

describe("background identity helpers", () => {
  it("recognizes legacy mayor identities across common address formats", () => {
    expect(isBackgroundIdentity("mayor")).toBe(true);
    expect(isBackgroundIdentity("Mayor")).toBe(true);
    expect(isBackgroundIdentity("rig/mayor")).toBe(true);
    expect(isBackgroundIdentity("gastown.mayor")).toBe(true);
    expect(isBackgroundIdentity("rig--mayor")).toBe(true);
    expect(isBackgroundIdentity("reviewer")).toBe(false);
    expect(isBackgroundIdentity("new-mayor")).toBe(false);
  });

  it("recognizes explicit background visibility markers", () => {
    expect(isBackgroundRecord({ name: "janitor", visibility: "background" })).toBe(true);
    expect(isBackgroundRecord({ name: "janitor", metadata: { visibility: "internal" } })).toBe(true);
    expect(isBackgroundRecord({ name: "reviewer", operator_visible: false })).toBe(true);
    expect(isBackgroundRecord({ name: "director" })).toBe(false);
  });

  it("redacts operator-facing labels without losing non-background addresses", () => {
    expect(formatOperatorAddress("gastown.mayor")).toBe("Internal");
    expect(formatOperatorAddress("rig/reviewer")).toBe("rig/reviewer");
    expect(hasBackgroundParticipant({ from: "director", to: "mayor" })).toBe(true);
    expect(hasBackgroundParticipant({ from: "director", to: "reviewer" })).toBe(false);
  });
});
