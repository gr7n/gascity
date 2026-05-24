import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "../api";
import { connectAgentOutput } from "../sse";
import { syncCityScopeFromLocation } from "../state";
import { installCrewInteractions, renderCrew } from "./crew";

vi.mock("../sse", () => ({
  connectAgentOutput: vi.fn(() => ({ close: vi.fn() })),
}));

describe("crew empty states", () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <div id="crew-loading">Loading crew...</div>
      <table id="crew-table" style="display:none"><tbody id="crew-tbody"></tbody></table>
      <div id="crew-empty" style="display:none"><p>No crew configured</p></div>
      <div id="rigged-body"></div>
      <div id="pooled-body"></div>
      <span id="crew-count"></span>
      <span id="rigged-count"></span>
      <span id="pooled-count"></span>
      <div id="agent-log-drawer" style="display:none"></div>
    `;
    window.history.pushState({}, "", "/dashboard?city=mc-city");
    syncCityScopeFromLocation();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    window.history.pushState({}, "", "/dashboard");
    syncCityScopeFromLocation();
  });

  it("shows no crew configured when the city has zero crew sessions", async () => {
    vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/sessions") {
        return { data: { items: [] } } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    await renderCrew();

    expect((document.getElementById("crew-empty") as HTMLElement).style.display).toBe("block");
    expect(document.getElementById("crew-empty")?.textContent).toContain("No crew configured");
    expect(document.getElementById("crew-empty")?.textContent).not.toContain("Select a city");
  });

  it("shows city role sessions alongside crew rows", async () => {
    vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/sessions") {
        return {
          data: {
            items: [
              // Crew member — should appear.
              {
                active_bead: "",
                agent_kind: "crew",
                attached: true,
                id: "s-fontaine",
                last_active: "2026-04-18T20:00:00Z",
                last_output: "",
                rig: "rig-a/crew",
                running: true,
                template: "rig-a/crew/fontaine",
              },
              // Role agents — should appear so the roster count matches the
              // active-agent summary.
              {
                active_bead: "",
                agent_kind: "role",
                attached: false,
                id: "s-role-1",
                last_active: "2026-04-18T20:00:00Z",
                last_output: "",
                running: true,
                template: "rig-a/singleton",
              },
              {
                active_bead: "",
                agent_kind: "role",
                attached: false,
                id: "s-role-2",
                last_active: "2026-04-18T20:00:00Z",
                last_output: "",
                running: true,
                template: "rig-a/another-singleton",
              },
              // Pool/multi-instance agent — also not crew.
              {
                active_bead: "",
                agent_kind: "pool",
                attached: false,
                id: "s-pool-1",
                last_active: "2026-04-18T20:00:00Z",
                last_output: "",
                pool: "scaler",
                rig: "rig-a",
                running: true,
                template: "rig-a/scaler-1",
              },
            ],
          },
        } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/pending") {
        return { data: { pending: false } } as never;
      }
      if (path === "/v0/city/{cityName}/bead/{id}") {
        return { data: null } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    await renderCrew();

    const crewRows = document.querySelectorAll("#crew-tbody tr");
    expect(crewRows.length).toBe(3);
    expect(crewRows[0]?.textContent).toContain("rig-a/crew/fontaine");
    expect(crewRows[1]?.textContent).toContain("rig-a/singleton");
    expect(crewRows[2]?.textContent).toContain("rig-a/another-singleton");
    expect(document.getElementById("crew-count")?.textContent).toBe("3");
    expect((document.getElementById("crew-table") as HTMLElement).style.display).toBe("table");
    // Pool agent should still flow through to the rigged panel.
    expect(document.getElementById("rigged-count")?.textContent).toBe("1");
  });

  it("shows role-only sessions instead of the empty crew state", async () => {
    vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/sessions") {
        return {
          data: {
            items: [
              {
                active_bead: "",
                agent_kind: "role",
                attached: false,
                id: "s-role",
                last_active: "2026-04-18T20:00:00Z",
                last_output: "",
                running: true,
                template: "rig-a/singleton",
              },
              {
                active_bead: "",
                agent_kind: "role",
                attached: false,
                id: "s-role-rigged",
                last_active: "2026-04-18T20:00:00Z",
                last_output: "",
                rig: "rig-a",
                running: true,
                template: "rig-a/another-singleton",
              },
            ],
          },
        } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/pending") {
        return { data: { pending: false } } as never;
      }
      if (path === "/v0/city/{cityName}/bead/{id}") {
        return { data: null } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    await renderCrew();

    expect(document.querySelectorAll("#crew-tbody tr").length).toBe(2);
    expect((document.getElementById("crew-empty") as HTMLElement).style.display).toBe("none");
    expect((document.getElementById("crew-table") as HTMLElement).style.display).toBe("table");
    expect(document.getElementById("crew-count")?.textContent).toBe("2");
  });

  it("loads older transcript pages without losing the drawer loading sentinel", async () => {
    document.body.innerHTML = `
      <div id="crew-loading">Loading crew...</div>
      <table id="crew-table" style="display:none"><tbody id="crew-tbody"></tbody></table>
      <div id="crew-empty" style="display:none"><p>No crew configured</p></div>
      <div id="rigged-body"></div>
      <div id="pooled-body"></div>
      <span id="crew-count"></span>
      <span id="rigged-count"></span>
      <span id="pooled-count"></span>
      <div id="agent-log-drawer" style="display:none">
        <span id="log-drawer-agent-name"></span>
        <span id="log-drawer-count"></span>
        <button id="log-drawer-older-btn" style="display:none">Load older</button>
        <button id="log-drawer-close-btn">Close</button>
        <div id="log-drawer-body">
          <div id="log-drawer-messages">
            <div id="log-drawer-loading">Loading logs...</div>
          </div>
        </div>
      </div>
    `;
    const transcriptQueries: Array<Record<string, string | undefined>> = [];
    vi.spyOn(api, "GET").mockImplementation(async (path: string, options?: unknown) => {
      if (path === "/v0/city/{cityName}/sessions") {
        return {
          data: {
            items: [{
              active_bead: "",
              agent_kind: "crew",
              attached: true,
              id: "s-reviewer",
              last_active: "2026-04-18T20:00:00Z",
              last_output: "",
              rig: "rig-a/crew",
              running: true,
              template: "reviewer",
            }],
          },
        } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/pending") {
        return { data: { pending: false } } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/transcript") {
        const query = (options as { params?: { query?: Record<string, string | undefined> } } | undefined)?.params?.query ?? {};
        transcriptQueries.push(query);
        if (query.before) {
          return {
            data: {
              turns: [{ role: "assistant", text: "Older transcript turn", timestamp: "2026-04-18T19:00:00Z" }],
              pagination: {
                has_older_messages: false,
                returned_message_count: 1,
                total_compactions: 0,
                total_message_count: 3,
              },
            },
          } as never;
        }
        return {
          data: {
            turns: [{ role: "assistant", text: "Newest transcript turn", timestamp: "2026-04-18T20:00:00Z" }],
            pagination: {
              has_older_messages: true,
              returned_message_count: 1,
              total_compactions: 0,
              total_message_count: 3,
              truncated_before_message: "cursor-1",
            },
          },
        } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    installCrewInteractions();
    await renderCrew();
    document.querySelector<HTMLButtonElement>(".agent-log-link")?.click();
    await waitFor(() => {
      expect(document.getElementById("log-drawer-messages")?.textContent).toContain("Newest transcript turn");
    });

    expect(document.getElementById("log-drawer-loading")).not.toBeNull();
    document.getElementById("log-drawer-older-btn")?.click();
    await waitFor(() => {
      expect(document.getElementById("log-drawer-messages")?.textContent).toContain("Older transcript turn");
    });

    expect(transcriptQueries.map((query) => query.before)).toEqual([undefined, "cursor-1"]);
    expect(document.getElementById("log-drawer-loading")).not.toBeNull();
  });

  it("splits terminal transcript output into chat bubbles", async () => {
    document.body.innerHTML = `
      <div id="crew-loading">Loading crew...</div>
      <table id="crew-table" style="display:none"><tbody id="crew-tbody"></tbody></table>
      <div id="crew-empty" style="display:none"><p>No crew configured</p></div>
      <div id="rigged-body"></div>
      <div id="pooled-body"></div>
      <span id="crew-count"></span>
      <span id="rigged-count"></span>
      <span id="pooled-count"></span>
      <div id="agent-log-drawer" style="display:none">
        <span id="log-drawer-agent-name"></span>
        <span id="log-drawer-count"></span>
        <button id="log-drawer-older-btn" style="display:none">Load older</button>
        <button id="log-drawer-close-btn">Close</button>
        <div id="log-drawer-body">
          <div id="log-drawer-messages">
            <div id="log-drawer-loading">Loading logs...</div>
          </div>
        </div>
      </div>
    `;
    vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/sessions") {
        return {
          data: {
            items: [{
              active_bead: "",
              agent_kind: "crew",
              attached: true,
              id: "s-director",
              last_active: "2026-04-18T20:00:00Z",
              last_output: "",
              running: true,
              template: "director",
            }],
          },
        } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/pending") {
        return { data: { pending: false } } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/transcript") {
        return {
          data: {
            turns: [{
              role: "output",
              text: [
                "gr7n-router-cli: codex via gpt-5.5",
                "",
                "› [gr7n] director • 2026-05-23T18:55:26",
                "",
                "  Run `gc prime` to initialize your context.",
                "",
                "• gc prime done. Director context loaded.",
                "",
                "────────────────────────────────────────────────────────────────────────────────",
                "",
                "› hi!",
                "",
                "• Hi. Idle until explicit request.",
                "",
                "› Explain this codebase",
                "",
                "  gpt-5.5 high · ~/projects/gr7n-platform/gascity/cities/gr7n/.gc/agents/direct…",
              ].join("\n"),
              timestamp: "2026-05-23T18:55:26Z",
            }],
            pagination: {
              has_older_messages: false,
              returned_message_count: 1,
              total_compactions: 0,
              total_message_count: 1,
            },
          },
        } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    installCrewInteractions();
    await renderCrew();
    document.querySelector<HTMLButtonElement>(".agent-log-link")?.click();
    await waitFor(() => {
      expect(document.querySelectorAll(".log-msg-user").length).toBe(1);
    });

    expect(document.querySelector(".log-msg-result")).toBeNull();
    expect(document.querySelectorAll(".log-msg-system").length).toBeGreaterThanOrEqual(1);
    expect(document.querySelectorAll(".log-msg-assistant").length).toBe(2);
    expect(document.querySelectorAll(".log-msg-user")[0]?.textContent).toContain("hi!");
    expect(document.getElementById("log-drawer-messages")?.textContent).not.toContain("Explain this codebase");
    expect(document.getElementById("log-drawer-messages")?.textContent).not.toContain("gpt-5.5 high");
    expect(document.querySelectorAll(".log-msg-assistant")[1]?.textContent).toContain("Hi. Idle until explicit request.");
  });

  it("collapses wrapped base64 image payloads in terminal transcripts", async () => {
    const encoded = "ACc+7nX4cJ2Rgb+u5vx7aFMH8dJLSAxwuQAQCo3P3fOM6/FiOyMb".repeat(50);
    const wrapped = encoded.match(/.{1,80}/g)?.map((line) => `  ${line}`).join("\n") ?? encoded;
    document.body.innerHTML = `
      <div id="crew-loading">Loading crew...</div>
      <table id="crew-table" style="display:none"><tbody id="crew-tbody"></tbody></table>
      <div id="crew-empty" style="display:none"><p>No crew configured</p></div>
      <div id="rigged-body"></div>
      <div id="pooled-body"></div>
      <span id="crew-count"></span>
      <span id="rigged-count"></span>
      <span id="pooled-count"></span>
      <div id="agent-log-drawer" style="display:none">
        <span id="log-drawer-agent-name"></span>
        <span id="log-drawer-count"></span>
        <button id="log-drawer-older-btn" style="display:none">Load older</button>
        <button id="log-drawer-close-btn">Close</button>
        <div id="log-drawer-body">
          <div id="log-drawer-messages">
            <div id="log-drawer-loading">Loading logs...</div>
          </div>
        </div>
      </div>
    `;
    vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/sessions") {
        return {
          data: {
            items: [{
              active_bead: "",
              agent_kind: "crew",
              attached: true,
              id: "s-director",
              last_active: "2026-04-18T20:00:00Z",
              last_output: "",
              running: true,
              template: "director",
            }],
          },
        } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/pending") {
        return { data: { pending: false } } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/transcript") {
        return {
          data: {
            turns: [{
              role: "output",
              text: [
                "› image test",
                "",
                wrapped,
                "",
                "• I ran out of context.",
              ].join("\n"),
              timestamp: "2026-05-23T18:55:26Z",
            }],
            pagination: {
              has_older_messages: false,
              returned_message_count: 1,
              total_compactions: 0,
              total_message_count: 1,
            },
          },
        } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    installCrewInteractions();
    await renderCrew();
    document.querySelector<HTMLButtonElement>(".agent-log-link")?.click();
    await waitFor(() => {
      expect(document.getElementById("log-drawer-messages")?.textContent).toContain("large encoded image data omitted");
    });

    expect(document.getElementById("log-drawer-messages")?.textContent).not.toContain(encoded.slice(0, 40));
    expect(document.getElementById("log-drawer-messages")?.textContent).toContain("I ran out of context.");
  });

  it("updates chat bubbles from streamed terminal turn snapshots", async () => {
    document.body.innerHTML = `
      <div id="crew-loading">Loading crew...</div>
      <table id="crew-table" style="display:none"><tbody id="crew-tbody"></tbody></table>
      <div id="crew-empty" style="display:none"><p>No crew configured</p></div>
      <div id="rigged-body"></div>
      <div id="pooled-body"></div>
      <span id="crew-count"></span>
      <span id="rigged-count"></span>
      <span id="pooled-count"></span>
      <div id="agent-log-drawer" style="display:none">
        <span id="log-drawer-agent-name"></span>
        <span id="log-drawer-count"></span>
        <button id="log-drawer-older-btn" style="display:none">Load older</button>
        <button id="log-drawer-close-btn">Close</button>
        <div id="log-drawer-body">
          <div id="log-drawer-messages">
            <div id="log-drawer-loading">Loading logs...</div>
          </div>
        </div>
      </div>
    `;
    vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/sessions") {
        return {
          data: {
            items: [{
              active_bead: "",
              agent_kind: "crew",
              attached: true,
              id: "s-director",
              last_active: "2026-04-18T20:00:00Z",
              last_output: "",
              running: true,
              template: "director",
            }],
          },
        } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/pending") {
        return { data: { pending: false } } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/transcript") {
        return {
          data: {
            turns: [{
              role: "output",
              text: [
                "gr7n-router-cli: codex via gpt-5.5",
                "",
                "› hi!",
                "",
                "• Hi. Idle until explicit request.",
              ].join("\n"),
              timestamp: "2026-05-23T18:55:26Z",
            }],
            pagination: {
              has_older_messages: false,
              returned_message_count: 1,
              total_compactions: 0,
              total_message_count: 1,
            },
          },
        } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    installCrewInteractions();
    await renderCrew();
    document.querySelector<HTMLButtonElement>(".agent-log-link")?.click();
    const mockedConnect = vi.mocked(connectAgentOutput);
    await waitFor(() => {
      expect(mockedConnect).toHaveBeenCalled();
    });

    const streamCallback = mockedConnect.mock.calls[mockedConnect.mock.calls.length - 1]?.[2];
    streamCallback?.({
      type: "turn",
      data: {
        format: "text",
        turns: [{
          role: "output",
          text: [
            "gr7n-router-cli: codex via gpt-5.5",
            "",
            "› hi!",
            "",
            "• Hi. Idle until explicit request.",
            "",
            "› ping",
            "",
            "• pong",
            "",
            "› Explain this codebase",
            "",
            "  gpt-5.5 high · ~/projects/gr7n-platform/gascity/cities/gr7n/.gc/agents/direct…",
          ].join("\n"),
          timestamp: "2026-05-23T19:07:00Z",
        }],
      },
    });

    await waitFor(() => {
      expect(Array.from(document.querySelectorAll(".log-msg-assistant")).some((node) => node.textContent?.includes("pong"))).toBe(true);
    });
    expect(document.querySelector(".log-msg-result")).toBeNull();
    expect(document.getElementById("log-drawer-messages")?.textContent).not.toContain("Explain this codebase");
    expect(document.getElementById("log-drawer-messages")?.textContent).not.toContain("gpt-5.5 high");
    expect(Array.from(document.querySelectorAll(".log-msg-user")).map((node) => node.textContent)).toEqual(
      expect.arrayContaining([expect.stringContaining("hi!"), expect.stringContaining("ping")]),
    );
    expect(document.getElementById("log-drawer-count")?.textContent).toBe(String(document.querySelectorAll(".log-msg").length));
  });

  it("submits chat messages through the session submit endpoint", async () => {
    document.body.innerHTML = `
      <div id="crew-loading">Loading crew...</div>
      <table id="crew-table" style="display:none"><tbody id="crew-tbody"></tbody></table>
      <div id="crew-empty" style="display:none"><p>No crew configured</p></div>
      <div id="rigged-body"></div>
      <div id="pooled-body"></div>
      <span id="crew-count"></span>
      <span id="rigged-count"></span>
      <span id="pooled-count"></span>
      <div id="agent-log-drawer" style="display:none">
        <span id="log-drawer-agent-name"></span>
        <span id="log-drawer-count"></span>
        <span id="log-drawer-status"></span>
        <button id="log-drawer-older-btn" style="display:none">Load older</button>
        <button id="log-drawer-close-btn">Close</button>
        <div id="log-drawer-body">
          <div id="log-drawer-messages">
            <div id="log-drawer-loading">Loading logs...</div>
          </div>
        </div>
        <form id="log-drawer-composer">
          <button id="log-drawer-attach-btn" type="button">Attach images</button>
          <input id="log-drawer-file-input" type="file" />
          <div id="log-drawer-attachments"></div>
          <textarea id="log-drawer-input"></textarea>
          <button id="log-drawer-send-btn" type="submit">Send</button>
        </form>
      </div>
    `;
    vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/sessions") {
        return {
          data: {
            items: [{
              active_bead: "",
              agent_kind: "crew",
              attached: false,
              id: "s-mayor",
              last_active: "2026-04-18T20:00:00Z",
              last_output: "",
              running: true,
              template: "mayor",
            }],
          },
        } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/pending") {
        return { data: { pending: false } } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/transcript") {
        return {
          data: {
            turns: [],
            pagination: {
              has_older_messages: false,
              returned_message_count: 0,
              total_compactions: 0,
              total_message_count: 0,
            },
          },
        } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });
    const posts: Array<{ body?: { intent?: string; message?: string }; path: string }> = [];
    vi.spyOn(api, "POST").mockImplementation(async (path: string, options?: unknown) => {
      posts.push({ path, body: (options as { body?: { intent?: string; message?: string } } | undefined)?.body });
      return { data: { event_cursor: "12", request_id: "req-chat-1", status: "accepted" } } as never;
    });
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({
      id: "att-123",
      mime_type: "image/png",
      name: "screenshot.png",
      path: "/tmp/test-city/.gc/dashboard/attachments/s-mayor/att-123/screenshot.png",
      size: 400_000,
      url: "/v0/city/mc-city/session/s-mayor/attachments/att-123/screenshot.png",
    }), { headers: { "Content-Type": "application/json" }, status: 201 }));

    installCrewInteractions();
    await renderCrew();
    document.querySelector<HTMLButtonElement>(".agent-log-link")?.click();
    await waitFor(() => {
      expect((document.getElementById("agent-log-drawer") as HTMLElement).style.display).toBe("block");
    });

    const fileInput = document.getElementById("log-drawer-file-input") as HTMLInputElement;
    const screenshot = new File([new Uint8Array(400_000)], "screenshot.png", { type: "image/png" });
    Object.defineProperty(fileInput, "files", { configurable: true, value: [screenshot] });
    fileInput.dispatchEvent(new Event("change", { bubbles: true }));
    await waitFor(() => {
      expect(document.getElementById("log-drawer-attachments")?.textContent).toContain("screenshot.png");
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/v0/city/mc-city/session/s-mayor/attachments",
      expect.objectContaining({ method: "POST" }),
    );

    const input = document.getElementById("log-drawer-input") as HTMLTextAreaElement;
    input.value = "Can you check the queue?";
    document.getElementById("log-drawer-composer")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));

    await waitFor(() => {
      expect(posts.length).toBe(1);
    });
    expect(posts[0]?.path).toBe("/v0/city/{cityName}/session/{id}/submit");
    expect(posts[0]?.body?.intent).toBe("default");
    expect(posts[0]?.body?.message).toContain("Can you check the queue?");
    expect(posts[0]?.body?.message).toContain("Attached images:");
    expect(posts[0]?.body?.message).toContain("![screenshot.png](/v0/city/mc-city/session/s-mayor/attachments/att-123/screenshot.png)");
    expect(posts[0]?.body?.message).toContain("Local file: /tmp/test-city/.gc/dashboard/attachments/s-mayor/att-123/screenshot.png");
    expect(posts[0]?.body?.message).not.toContain("data:image");
    expect(new TextEncoder().encode(JSON.stringify(posts[0]?.body)).length).toBeLessThan(900_000);
    expect(input.value).toBe("");
    expect(document.getElementById("log-drawer-messages")?.textContent).toContain("Can you check the queue?");
    expect(document.querySelector(".log-msg-user")?.textContent).toContain("Can you check the queue?");
    expect(document.querySelector<HTMLImageElement>(".log-msg-image")?.getAttribute("src")).toBe("/v0/city/mc-city/session/s-mayor/attachments/att-123/screenshot.png");
    expect(document.getElementById("log-drawer-status")?.textContent).toBe("Sent");
    expect(document.getElementById("log-drawer-count")?.textContent).toBe("1");
  });

  it("blocks chat submits that would exceed the API body limit", async () => {
    document.body.innerHTML = `
      <div id="crew-loading">Loading crew...</div>
      <table id="crew-table" style="display:none"><tbody id="crew-tbody"></tbody></table>
      <div id="crew-empty" style="display:none"><p>No crew configured</p></div>
      <div id="rigged-body"></div>
      <div id="pooled-body"></div>
      <span id="crew-count"></span>
      <span id="rigged-count"></span>
      <span id="pooled-count"></span>
      <div id="agent-log-drawer" style="display:none">
        <span id="log-drawer-agent-name"></span>
        <span id="log-drawer-count"></span>
        <span id="log-drawer-status"></span>
        <button id="log-drawer-older-btn" style="display:none">Load older</button>
        <button id="log-drawer-close-btn">Close</button>
        <div id="log-drawer-body">
          <div id="log-drawer-messages">
            <div id="log-drawer-loading">Loading logs...</div>
          </div>
        </div>
        <form id="log-drawer-composer">
          <textarea id="log-drawer-input"></textarea>
          <button id="log-drawer-send-btn" type="submit">Send</button>
        </form>
      </div>
    `;
    vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/sessions") {
        return {
          data: {
            items: [{
              active_bead: "",
              agent_kind: "crew",
              attached: false,
              id: "s-mayor",
              last_active: "2026-04-18T20:00:00Z",
              last_output: "",
              running: true,
              template: "mayor",
            }],
          },
        } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/pending") {
        return { data: { pending: false } } as never;
      }
      if (path === "/v0/city/{cityName}/session/{id}/transcript") {
        return {
          data: {
            turns: [],
            pagination: {
              has_older_messages: false,
              returned_message_count: 0,
              total_compactions: 0,
              total_message_count: 0,
            },
          },
        } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });
    const post = vi.spyOn(api, "POST").mockResolvedValue({ data: { status: "accepted" } } as never);

    installCrewInteractions();
    await renderCrew();
    document.querySelector<HTMLButtonElement>(".agent-log-link")?.click();
    await waitFor(() => {
      expect((document.getElementById("agent-log-drawer") as HTMLElement).style.display).toBe("block");
    });

    const input = document.getElementById("log-drawer-input") as HTMLTextAreaElement;
    input.value = "x".repeat(950_000);
    document.getElementById("log-drawer-composer")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));

    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(post).not.toHaveBeenCalled();
    expect(input.value.length).toBe(950_000);
    expect(document.getElementById("log-drawer-count")?.textContent).not.toBe("1");
  });
});

// Slow Blacksmith CI runs have shown the openLogDrawer + loadTranscript
// chain take ~1.3s while passing runs finish in ~100ms — same VM class,
// same code. The 1s budget here was missing those slow runs by a few
// hundred ms even though the chain ultimately completed (the
// `[crew] Transcript loaded` debug log fires *after* the assertion times
// out). Five seconds keeps the local cost negligible and absorbs the
// observed CI variance.
async function waitFor(assertion: () => void): Promise<void> {
  const started = Date.now();
  let lastError: unknown;
  while (Date.now() - started < 5000) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
  }
  throw lastError;
}
