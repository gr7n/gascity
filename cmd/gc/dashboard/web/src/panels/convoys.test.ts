import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api, type BeadRecord } from "../api";
import { renderConvoys } from "./convoys";

vi.mock("../api", () => ({
  api: {
    GET: vi.fn(),
    POST: vi.fn(),
  },
  cityScope: vi.fn(() => "test-city"),
  mutationHeaders: { "X-GC-Request": "true" },
}));

vi.mock("../ui", () => ({
  popPause: vi.fn(),
  pushPause: vi.fn(),
  showToast: vi.fn(),
}));

const getMock = api.GET as unknown as ReturnType<typeof vi.fn>;

function installDOM(): void {
  document.body.innerHTML = `
    <span id="convoy-count"></span>
    <div id="convoy-list"></div>
  `;
}

function convoy(overrides: Partial<BeadRecord> = {}): BeadRecord {
  return {
    created_at: "2026-06-20T12:00:00Z",
    id: "convoy-1",
    issue_type: "convoy",
    status: "open",
    title: "Demo convoy",
    ...overrides,
  };
}

describe("convoy list rendering", () => {
  beforeEach(() => {
    getMock.mockReset();
    installDOM();
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("renders convoy rows without fetching each convoy detail", async () => {
    getMock.mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/convoys") {
        return {
          data: {
            items: [
              convoy({ id: "convoy-1", title: "First convoy" }),
              convoy({ id: "convoy-2", title: "Second convoy" }),
            ],
          },
          error: undefined,
          request: undefined,
          response: undefined,
        };
      }
      throw new Error(`unexpected GET ${path}`);
    });

    await renderConvoys();

    expect(document.getElementById("convoy-count")?.textContent).toBe("2");
    expect([...document.querySelectorAll(".convoy-title")].map((node) => node.textContent)).toEqual([
      "First convoy",
      "Second convoy",
    ]);
    expect(getMock).toHaveBeenCalledTimes(1);
    expect(getMock).toHaveBeenCalledWith("/v0/city/{cityName}/convoys", {
      params: { path: { cityName: "test-city" }, query: { limit: 200 } },
    });
  });

  it("redacts background assignees in convoy rows", async () => {
    getMock.mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/convoys") {
        return {
          data: { items: [convoy({ assignee: "rig/infra-worker" })] },
          error: undefined,
          request: undefined,
          response: undefined,
        };
      }
      throw new Error(`unexpected GET ${path}`);
    });

    await renderConvoys();

    expect(document.body.textContent).toContain("Internal");
    expect(document.body.textContent).not.toContain("infra-worker");
  });
});
