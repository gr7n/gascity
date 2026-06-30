import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "../api";
import { installCommandPalette } from "../palette";
import { syncCityScopeFromLocation } from "../state";
import { installMailInteractions, openMailComposer, renderMail } from "./mail";
import * as options from "./options";

describe("mail compose flows", () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <div id="toast-container"></div>
      <button id="open-palette-btn" type="button">Open</button>
      <div id="command-palette-overlay">
        <div class="command-palette">
          <input id="command-palette-input" />
          <div id="command-palette-results"></div>
        </div>
      </div>
      <div id="output-panel">
        <span id="output-panel-cmd"></span>
        <div id="output-panel-content"></div>
        <button id="output-close-btn" type="button"></button>
        <button id="output-copy-btn" type="button"></button>
      </div>
      <button id="compose-mail-btn" type="button">Compose</button>
      <button id="mail-back-btn" type="button">Back</button>
      <button id="compose-back-btn" type="button">Back</button>
      <button id="compose-cancel-btn" type="button">Cancel</button>
      <button id="mail-reply-btn" type="button">Reply</button>
      <button id="mail-send-btn" type="button">Send</button>
      <button id="mail-archive-btn" type="button">Archive</button>
      <button id="mail-toggle-unread-btn" type="button">Unread</button>
      <div id="mail-list"></div>
      <div id="mail-all" style="display:none"></div>
      <div id="mail-detail" style="display:none"></div>
      <div id="mail-compose" style="display:none">
        <select id="compose-to"></select>
        <input id="compose-subject" />
        <textarea id="compose-body"></textarea>
        <input id="compose-reply-to" />
        <span id="mail-compose-title"></span>
      </div>
    `;
    window.history.pushState({}, "", "/dashboard?city=mc-city");
    syncCityScopeFromLocation();
    vi.spyOn(options, "getOptions").mockResolvedValue({
      agents: ["director"],
      beads: [],
      fetchedAt: Date.now(),
      mail: [],
      rigs: ["city"],
      sessions: [{ id: "mc-vv8", label: "director", recipient: "director" }],
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    window.history.pushState({}, "", "/dashboard");
    syncCityScopeFromLocation();
  });

  it("opens the compose form from the command palette", async () => {
    installMailInteractions();
    installCommandPalette({ refreshAll: vi.fn().mockResolvedValue(undefined) });

    (document.getElementById("open-palette-btn") as HTMLButtonElement).click();
    const input = document.getElementById("command-palette-input") as HTMLInputElement;
    input.value = "compose";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "Enter" }));
    await Promise.resolve();

    expect((document.getElementById("mail-compose") as HTMLElement).style.display).toBe("block");
    expect((document.getElementById("mail-compose-title") as HTMLElement).textContent).toBe("New Message");
    const values = [...(document.getElementById("compose-to") as HTMLSelectElement).options].map((option) => option.value);
    expect(values).toContain("director");
    expect(values).not.toContain("mc-vv8");
  });

  it("sends mail to the recipient name instead of the session id", async () => {
    vi.spyOn(api, "POST").mockImplementation(async (path: string, init?: { body?: unknown }) => {
      if (path === "/v0/city/{cityName}/mail") {
        return { data: { id: "mail-1" }, error: undefined, request: undefined, response: undefined } as never;
      }
      throw new Error(`unexpected POST ${path} ${JSON.stringify(init?.body)}`);
    });
    vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/mail") {
        return { data: { items: [] }, error: undefined, request: undefined, response: undefined } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    installMailInteractions();
    await openMailComposer();
    (document.getElementById("compose-to") as HTMLSelectElement).value = "director";
    (document.getElementById("compose-subject") as HTMLInputElement).value = "hello";
    (document.getElementById("compose-body") as HTMLTextAreaElement).value = "greetings";

    (document.getElementById("mail-send-btn") as HTMLButtonElement).click();
    await Promise.resolve();
    await Promise.resolve();

    expect(api.POST).toHaveBeenCalledWith(
      "/v0/city/{cityName}/mail",
      expect.objectContaining({
        body: expect.objectContaining({ to: "director" }),
      }),
    );
  });

  it("does not add a background sender as a reply recipient", async () => {
    installMailInteractions();
    await openMailComposer({
      created_at: "2026-01-01T00:00:00Z",
      from: "gastown.mayor",
      id: "mail-1",
      read: false,
      subject: "Internal note",
      to: "director",
    } as never);

    const values = [...(document.getElementById("compose-to") as HTMLSelectElement).options].map((option) => option.value);
    expect(values).toContain("director");
    expect(values).not.toContain("gastown.mayor");
    expect((document.getElementById("compose-to") as HTMLSelectElement).value).toBe("");
  });
});

describe("mail detail visibility", () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <div id="toast-container"></div>
      <button id="compose-mail-btn" type="button">Compose</button>
      <button id="mail-back-btn" type="button">Back</button>
      <button id="compose-back-btn" type="button">Back</button>
      <button id="compose-cancel-btn" type="button">Cancel</button>
      <button id="mail-reply-btn" type="button">Reply</button>
      <button id="mail-send-btn" type="button">Send</button>
      <button id="mail-archive-btn" type="button">Archive</button>
      <button id="mail-toggle-unread-btn" type="button">Unread</button>
      <span id="mail-count"></span>
      <div id="mail-loading"></div>
      <div id="mail-empty"><p></p></div>
      <div id="mail-list">
        <div id="mail-threads"></div>
      </div>
      <div id="mail-all" style="display:none"></div>
      <div id="mail-detail" style="display:none">
        <div id="mail-detail-subject"></div>
        <div id="mail-detail-from"></div>
        <div id="mail-detail-time"></div>
        <div id="mail-detail-body"></div>
      </div>
      <div id="mail-compose" style="display:none">
        <select id="compose-to"></select>
        <input id="compose-subject" />
        <textarea id="compose-body"></textarea>
        <input id="compose-reply-to" />
        <span id="mail-compose-title"></span>
      </div>
    `;
    window.history.pushState({}, "", "/dashboard?city=mc-city");
    syncCityScopeFromLocation();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    window.history.pushState({}, "", "/dashboard");
    syncCityScopeFromLocation();
  });

  it("filters background participants from thread detail responses", async () => {
    vi.spyOn(api, "GET").mockImplementation(async (path: string) => {
      if (path === "/v0/city/{cityName}/mail") {
        return {
          data: {
            items: [{
              body: "Visible update",
              created_at: "2026-01-01T00:00:02Z",
              from: "reviewer",
              id: "mail-visible",
              read: false,
              subject: "Visible thread",
              thread_id: "thread-1",
              to: "director",
            }],
          },
          error: undefined,
          request: undefined,
          response: undefined,
        } as never;
      }
      if (path === "/v0/city/{cityName}/mail/thread/{id}") {
        return {
          data: {
            items: [
              {
                body: "Internal background note",
                created_at: "2026-01-01T00:00:01Z",
                from: "gastown.mayor",
                id: "mail-background",
                read: true,
                subject: "Internal note",
                thread_id: "thread-1",
                to: "director",
              },
              {
                body: "Visible update",
                created_at: "2026-01-01T00:00:02Z",
                from: "reviewer",
                id: "mail-visible",
                read: false,
                subject: "Visible thread",
                thread_id: "thread-1",
                to: "director",
              },
            ],
          },
          error: undefined,
          request: undefined,
          response: undefined,
        } as never;
      }
      throw new Error(`unexpected GET ${path}`);
    });

    installMailInteractions();
    await renderMail();
    (document.querySelector(".mail-thread") as HTMLElement).click();
    await Promise.resolve();
    await Promise.resolve();

    const detailText = (document.getElementById("mail-detail") as HTMLElement).textContent ?? "";
    expect(detailText).toContain("Visible update");
    expect(detailText).toContain("reviewer");
    expect(detailText).not.toContain("Internal background note");
    expect(detailText).not.toContain("gastown.mayor");
  });
});
