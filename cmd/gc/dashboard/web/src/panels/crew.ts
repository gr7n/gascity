import type { SessionRecord } from "../api";
import { api, cityScope, mutationHeaders } from "../api";
import { byId, clear, el } from "../util/dom";
import { calculateActivity, formatTimestamp, statusBadgeClass, truncate } from "../util/legacy";
import { connectAgentOutput, type AgentOutputMessage, type SSEHandle } from "../sse";
import { popPause, pushPause, showToast } from "../ui";
import { logDebug } from "../logger";

let logHandle: SSEHandle | null = null;
let logSessionID = "";
let logBeforeCursor = "";
let logCount = 0;
let logSubmitting = false;

interface ChatAttachment {
  dataURL: string;
  id: string;
  name: string;
  size: number;
  type: string;
}

interface DisplayTurn {
  role: string;
  text: string;
  timestamp?: string;
}

interface TranscriptTurn {
  role?: string;
  text?: string;
  timestamp?: string;
}

interface StreamTurnPayload {
  data?: { message?: TranscriptTurn };
  event?: string;
  format?: string;
  turns?: TranscriptTurn[];
}

const SESSION_SUBMIT_BODY_LIMIT_BYTES = 1_048_576;
const SESSION_SUBMIT_SAFE_BYTES = 900_000;
let pendingAttachments: ChatAttachment[] = [];

export async function renderCrew(): Promise<void> {
  const city = cityScope();
  if (!city) {
    resetCrewNoCity();
    return;
  }

  const crewLoading = byId("crew-loading");
  const crewTable = byId<HTMLTableElement>("crew-table");
  const crewEmpty = byId("crew-empty");
  const crewBody = byId("crew-tbody");
  const riggedBody = byId("rigged-body");
  const pooledBody = byId("pooled-body");
  if (!crewLoading || !crewTable || !crewEmpty || !crewBody || !riggedBody || !pooledBody) return;

  setCrewEmptyMessage("No crew configured");
  crewLoading.style.display = "block";
  crewTable.style.display = "none";
  crewEmpty.style.display = "none";
  clear(crewBody);

  const { data, error } = await api.GET("/v0/city/{cityName}/sessions", {
    params: { path: { cityName: city }, query: { state: "active", peek: true } },
  });
  if (error || !data?.items) {
    crewLoading.textContent = "Failed to load crew";
    renderSimpleEmpty(riggedBody, "No rigged agents");
    renderSimpleEmpty(pooledBody, "No pooled agents");
    return;
  }

  const sessions = data.items;
  // The Crew table is the city-visible roster. Include persistent crew plus
  // city role sessions such as mayor/director so the panel count lines up
  // with the header's active-agent count instead of hiding role agents.
  const crew = sessions.filter(
    (session) => session.agent_kind === "crew" || session.agent_kind === "role",
  );
  const pending = await Promise.all(
    crew.map(async (session) => {
      const res = await api.GET("/v0/city/{cityName}/session/{id}/pending", {
        params: { path: { cityName: city, id: session.id } },
      });
      return Boolean(res.data?.pending);
    }),
  );

  const beadTitles = new Map<string, string>();
  await Promise.all(
    sessions.map(async (session) => {
      if (!session.active_bead) return;
      if (beadTitles.has(session.active_bead)) return;
      const res = await api.GET("/v0/city/{cityName}/bead/{id}", {
        params: { path: { cityName: city, id: session.active_bead } },
      });
      beadTitles.set(session.active_bead, res.data?.id ? (res.data.title ?? res.data.id) : session.active_bead);
    }),
  );

  crew.forEach((session, index) => {
    const state = classifyCrewState(session, pending[index] ?? false);
    const beadText = session.active_bead ? truncate(beadTitles.get(session.active_bead) ?? session.active_bead, 24) : "—";
    const row = el("tr", {}, [
      el("td", {}, [session.template]),
      el("td", {}, [session.rig ?? "city"]),
      el("td", {}, [el("span", { class: `badge ${statusBadgeClass(state)}` }, [state])]),
      el("td", {}, [beadText]),
      el("td", { class: calculateActivity(session.last_active).colorClass ? `activity-${calculateActivity(session.last_active).colorClass}` : "" }, [
        el("span", { class: "activity-dot" }),
        ` ${calculateActivity(session.last_active).display}`,
      ]),
      el("td", {}, [
        el("span", { class: `badge ${session.attached ? "badge-green" : "badge-muted"}` }, [
          session.attached ? "Attached" : "Detached",
        ]),
      ]),
      el("td", {}, [
        chatButton(session.id, session.template),
        " ",
        attachButton(session.template),
      ]),
    ]);
    crewBody.append(row);
  });

  byId("crew-count")!.textContent = String(crew.length);
  crewLoading.style.display = "none";
  if (crew.length > 0) {
    crewTable.style.display = "table";
  } else {
    setCrewEmptyMessage("No crew configured");
    crewEmpty.style.display = "block";
  }

  renderRiggedAgents(sessions, beadTitles);
  renderPooledAgents(sessions);
}

export function resetCrewNoCity(): void {
  const crewLoading = byId("crew-loading");
  const crewTable = byId<HTMLTableElement>("crew-table");
  const crewEmpty = byId("crew-empty");
  const crewBody = byId("crew-tbody");
  const riggedBody = byId("rigged-body");
  const pooledBody = byId("pooled-body");
  if (!crewLoading || !crewTable || !crewEmpty || !crewBody || !riggedBody || !pooledBody) return;

  closeLogDrawer();
  byId("crew-count")!.textContent = "0";
  byId("rigged-count")!.textContent = "0";
  byId("pooled-count")!.textContent = "0";
  crewLoading.style.display = "none";
  crewTable.style.display = "none";
  crewEmpty.style.display = "block";
  setCrewEmptyMessage("Select a city to view crew");
  clear(crewBody);
  renderSimpleEmpty(riggedBody, "Select a city to view rigged agents");
  renderSimpleEmpty(pooledBody, "Select a city to view pooled agents");
}

function setCrewEmptyMessage(message: string): void {
  byId("crew-empty")?.querySelector("p")?.replaceChildren(document.createTextNode(message));
}

function classifyCrewState(session: SessionRecord, hasPending: boolean): string {
  if (hasPending) return "questions";
  if (session.active_bead) return "spinning";
  if (!session.running) return "finished";
  return "idle";
}

function attachButton(template: string): HTMLElement {
  const btn = el("button", { class: "attach-btn", type: "button" }, ["Terminal"]);
  btn.addEventListener("click", async () => {
    const command = `gc agent attach ${template}`;
    try {
      await navigator.clipboard.writeText(command);
      showToast("success", "Attach command copied", command);
    } catch {
      showToast("error", "Copy failed", command);
    }
  });
  return btn;
}

function chatButton(sessionID: string, label: string): HTMLElement {
  return logButton(sessionID, "Chat", label);
}

function logButton(sessionID: string, label: string, title = label): HTMLElement {
  const btn = el("button", { class: "agent-log-link", type: "button", "data-session-id": sessionID, title }, [label]);
  btn.addEventListener("click", () => {
    void openLogDrawer(sessionID, title);
  });
  return btn;
}

// renderRiggedAgents lists sessions attached to a specific rig. Grouping
// is purely by the API's `rig` + `pool` fields — no role names hardcoded.
function renderRiggedAgents(sessions: SessionRecord[], beadTitles: Map<string, string>): void {
  const body = byId("rigged-body");
  const count = byId("rigged-count");
  if (!body || !count) return;

  const rows = sessions.filter((session) => session.rig && session.pool);
  count.textContent = String(rows.length);
  if (rows.length === 0) {
    renderSimpleEmpty(body, "No rigged agents");
    return;
  }

  const tbody = el("tbody");
  rows.forEach((session) => {
    const activity = calculateActivity(session.last_active);
    const workStatus = !session.active_bead ? "Idle" : activity.colorClass === "red" ? "Stuck" : activity.colorClass === "yellow" ? "Stale" : "Working";
    tbody.append(el("tr", { class: `rigged-${workStatus.toLowerCase()}` }, [
      el("td", {}, [logButton(session.id, session.template)]),
      el("td", {}, [el("span", { class: "badge badge-muted" }, [session.pool ?? "pool"])]),
      el("td", {}, [session.rig ?? "city"]),
      el("td", { class: "rigged-issue" }, [
        session.active_bead
          ? `${session.active_bead} ${beadTitles.get(session.active_bead) ?? ""}`.trim()
          : "—",
      ]),
      el("td", {}, [el("span", { class: `badge ${statusBadgeClass(workStatus)}` }, [workStatus])]),
      el("td", { class: `activity-${activity.colorClass}` }, [el("span", { class: "activity-dot" }), ` ${activity.display}`]),
    ]));
  });

  clear(body);
  body.append(el("table", {}, [
    el("thead", {}, [el("tr", {}, [
      el("th", {}, ["Agent"]),
      el("th", {}, ["Pool"]),
      el("th", {}, ["Rig"]),
      el("th", {}, ["Working On"]),
      el("th", {}, ["Status"]),
      el("th", {}, ["Activity"]),
    ])]),
    tbody,
  ]));
}

// renderPooledAgents lists sessions that belong to a pool but are not
// bound to a specific rig (floating workers). Grouping is by API fields
// only — no role names hardcoded.
function renderPooledAgents(sessions: SessionRecord[]): void {
  const body = byId("pooled-body");
  const count = byId("pooled-count");
  if (!body || !count) return;
  const rows = sessions.filter((session) => !session.rig && session.pool);
  count.textContent = String(rows.length);
  if (rows.length === 0) {
    renderSimpleEmpty(body, "No pooled agents");
    return;
  }

  const tbody = el("tbody");
  rows.forEach((session) => {
    tbody.append(el("tr", {}, [
      el("td", {}, [session.template]),
      el("td", {}, [el("span", { class: `badge ${session.active_bead ? "badge-yellow" : "badge-green"}` }, [session.active_bead ? "Working" : "Idle"])]),
      el("td", { class: "status-hint" }, [truncate(session.last_output, 80) || "—"]),
      el("td", {}, [formatTimestamp(session.last_active)]),
    ]));
  });

  clear(body);
  body.append(el("table", {}, [
    el("thead", {}, [el("tr", {}, [
      el("th", {}, ["Agent"]),
      el("th", {}, ["State"]),
      el("th", {}, ["Work"]),
      el("th", {}, ["Activity"]),
    ])]),
    tbody,
  ]));
}

function renderSimpleEmpty(container: HTMLElement, message: string): void {
  clear(container);
  container.append(el("div", { class: "empty-state" }, [el("p", {}, [message])]));
}

export function installCrewInteractions(): void {
  byId("log-drawer-close-btn")?.addEventListener("click", () => closeLogDrawer());
  const attachBtn = byId<HTMLButtonElement>("log-drawer-attach-btn");
  if (attachBtn) {
    attachBtn.title = "Image upload needs a server attachment endpoint before it can be sent safely";
    attachBtn.addEventListener("click", () => showImageUploadUnsupported());
  }
  byId<HTMLInputElement>("log-drawer-file-input")?.addEventListener("change", (event) => {
    const input = event.currentTarget;
    if (!(input instanceof HTMLInputElement)) return;
    void addSelectedAttachments(input.files);
    input.value = "";
  });
  byId<HTMLFormElement>("log-drawer-composer")?.addEventListener("submit", (event) => {
    event.preventDefault();
    void submitLogDrawerMessage();
  });
  byId<HTMLTextAreaElement>("log-drawer-input")?.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" || event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) return;
    event.preventDefault();
    void submitLogDrawerMessage();
  });
  byId<HTMLTextAreaElement>("log-drawer-input")?.addEventListener("paste", (event) => {
    const imageFiles = Array.from(event.clipboardData?.files ?? []).filter((file) => file.type.startsWith("image/"));
    if (imageFiles.length === 0) return;
    event.preventDefault();
    showImageUploadUnsupported();
  });
  byId("log-drawer-older-btn")?.addEventListener("click", () => {
    logDebug("crew", "Load older transcript clicked", {
      hasCursor: logBeforeCursor !== "",
      sessionID: logSessionID,
    });
    if (!logSessionID || !logBeforeCursor) return;
    void loadTranscript(logSessionID, true);
  });
}

async function openLogDrawer(sessionID: string, label: string): Promise<void> {
  const drawer = byId("agent-log-drawer");
  const nameEl = byId("log-drawer-agent-name");
  const messagesEl = byId("log-drawer-messages");
  const loadingEl = byId("log-drawer-loading");
  if (!drawer || !nameEl || !messagesEl || !loadingEl) return;

  if (logSessionID === sessionID && drawer.style.display !== "none") {
    closeLogDrawer();
    return;
  }

  closeLogDrawer();
  logSessionID = sessionID;
  logBeforeCursor = "";
  logCount = 0;

  nameEl.textContent = label;
  clear(messagesEl);
  messagesEl.append(loadingEl);
  loadingEl.style.display = "block";
  resetLogComposer();
  drawer.style.display = "block";
  pushPause();

  await loadTranscript(sessionID, false);
  const city = cityScope();
  if (!city) return;
  logHandle = connectAgentOutput(city, sessionID, (msg) => appendStreamEvent(msg));
}

function closeLogDrawer(): void {
  logHandle?.close();
  logHandle = null;
  logSessionID = "";
  logBeforeCursor = "";
  logSubmitting = false;
  resetLogComposer();
  const drawer = byId("agent-log-drawer");
  if (drawer && drawer.style.display !== "none") {
    drawer.style.display = "none";
    popPause();
  }
}

// closeLogDrawerExternal is called by main.ts when the dashboard leaves
// city scope, so the transcript stream + its `pushPause()` token get
// torn down along with every other city-scoped panel. Without this, a
// drawer open at scope-change time would keep its session stream alive
// and leave `pauseCount > 0` forever (blocking all refreshes).
export function closeLogDrawerExternal(): void {
  closeLogDrawer();
}

async function loadTranscript(sessionID: string, prepend: boolean): Promise<void> {
  const city = cityScope();
  const messagesEl = byId("log-drawer-messages");
  const loadingEl = byId("log-drawer-loading");
  const olderBtn = byId<HTMLButtonElement>("log-drawer-older-btn");
  const countEl = byId("log-drawer-count");
  const body = byId("log-drawer-body");
  if (!city || !messagesEl || !loadingEl || !olderBtn || !countEl) return;

  const previousScrollHeight = body?.scrollHeight ?? 0;
  const previousScrollTop = body?.scrollTop ?? 0;
  loadingEl.style.display = "block";
  const res = await api.GET("/v0/city/{cityName}/session/{id}/transcript", {
    params: {
      path: { cityName: city, id: sessionID },
      query: { tail: String(prepend ? 50 : 25), before: prepend ? logBeforeCursor : undefined },
    },
  });
  loadingEl.style.display = "none";
  if (res.error || !res.data) {
    showToast("error", "Transcript failed", res.error?.detail ?? "Could not load transcript");
    return;
  }

  const fragment = document.createDocumentFragment();
  logCount += appendDisplayTurns(fragment, expandTranscriptTurns(res.data.turns ?? []));
  if (prepend) {
    messagesEl.prepend(fragment);
  } else {
    clear(messagesEl);
    messagesEl.append(fragment);
  }
  messagesEl.append(loadingEl);
  loadingEl.style.display = "none";
  countEl.textContent = String(logCount);

  logBeforeCursor = res.data.pagination?.truncated_before_message ?? "";
  olderBtn.style.display = res.data.pagination?.has_older_messages && logBeforeCursor ? "inline-flex" : "none";
  if (prepend && body) {
    body.scrollTop = body.scrollHeight - previousScrollHeight + previousScrollTop;
  } else {
    scrollLogDrawerToBottom();
    byId<HTMLTextAreaElement>("log-drawer-input")?.focus();
  }
  logDebug("crew", "Transcript loaded", {
    hasOlderMessages: res.data.pagination?.has_older_messages ?? false,
    nextBeforeCursor: logBeforeCursor,
    prepend,
    sessionID,
    turnCount: res.data.turns?.length ?? 0,
  });
}

function appendStreamEvent(msg: AgentOutputMessage): void {
  const messagesEl = byId("log-drawer-messages");
  if (!messagesEl) return;
  const payload = msg.data as StreamTurnPayload | null;
  if ((msg.type === "turn" || msg.type === "message") && Array.isArray(payload?.turns)) {
    if (shouldReplaceWithStreamSnapshot(payload)) {
      replaceTranscriptTurns(payload.turns);
      return;
    }
    logCount += appendDisplayTurns(messagesEl, expandTranscriptTurns(payload.turns));
    updateLogCount();
    scrollLogDrawerToBottom();
    return;
  }
  if (msg.type !== "message" || !payload?.data?.message) return;
  logCount += appendDisplayTurns(messagesEl, expandTranscriptTurns([payload.data.message]));
  updateLogCount();
  scrollLogDrawerToBottom();
}

async function submitLogDrawerMessage(): Promise<void> {
  const city = cityScope();
  const input = byId<HTMLTextAreaElement>("log-drawer-input");
  const sendBtn = byId<HTMLButtonElement>("log-drawer-send-btn");
  const statusEl = byId("log-drawer-status");
  const sessionID = logSessionID;
  const message = input?.value.trim() ?? "";
  const attachments = [...pendingAttachments];
  if (!city || !sessionID || !input || !sendBtn || logSubmitting) return;
  if (!message && attachments.length === 0) {
    input.focus();
    return;
  }
  if (attachments.length > 0) {
    showImageUploadUnsupported();
    input.focus();
    return;
  }
  const submitMessage = buildSubmitMessage(message, attachments);
  const submitBytes = submitRequestBytes(submitMessage);
  if (submitBytes > SESSION_SUBMIT_SAFE_BYTES) {
    statusEl?.replaceChildren(document.createTextNode(""));
    showToast(
      "error",
      "Message too large",
      `Remove an image or shorten the message (${formatBytes(submitBytes)} / ${formatBytes(SESSION_SUBMIT_BODY_LIMIT_BYTES)})`,
    );
    input.focus();
    return;
  }

  logSubmitting = true;
  sendBtn.disabled = true;
  statusEl?.replaceChildren(document.createTextNode("Sending..."));
  const res = await api.POST("/v0/city/{cityName}/session/{id}/submit", {
    params: { path: { cityName: city, id: sessionID }, header: mutationHeaders },
    body: { intent: "default", message: submitMessage },
  });
  logSubmitting = false;
  sendBtn.disabled = false;

  if (res.error) {
    statusEl?.replaceChildren(document.createTextNode(""));
    showToast("error", "Message failed", res.error.detail ?? "Could not submit message");
    input.focus();
    return;
  }

  input.value = "";
  pendingAttachments = [];
  renderPendingAttachments();
  appendLocalTurn("user", message, attachments);
  statusEl?.replaceChildren(document.createTextNode("Sent"));
  showToast("success", "Message sent", res.data?.request_id ?? sessionID);
  input.focus();
}

function appendLocalTurn(role: string, text: string, attachments: ChatAttachment[] = []): void {
  const messagesEl = byId("log-drawer-messages");
  if (!messagesEl) return;
  messagesEl.append(renderTurn(role, text, new Date().toISOString(), attachments));
  logCount += 1;
  updateLogCount();
  scrollLogDrawerToBottom();
}

function resetLogComposer(): void {
  const input = byId<HTMLTextAreaElement>("log-drawer-input");
  const sendBtn = byId<HTMLButtonElement>("log-drawer-send-btn");
  if (input) input.value = "";
  if (sendBtn) sendBtn.disabled = false;
  pendingAttachments = [];
  renderPendingAttachments();
  byId("log-drawer-status")?.replaceChildren(document.createTextNode(""));
}

async function addSelectedAttachments(files: FileList | File[] | null): Promise<void> {
  if (!files) return;
  if (Array.from(files).some((file) => file.type.startsWith("image/"))) {
    showImageUploadUnsupported();
  }
}

function showImageUploadUnsupported(): void {
  showToast("error", "Images not sent", "Session chat needs a real attachment endpoint before images are safe to deliver");
}

function submitRequestBytes(message: string): number {
  return utf8Bytes(JSON.stringify({ intent: "default", message }));
}

function utf8Bytes(value: string): number {
  return new TextEncoder().encode(value).length;
}

function formatBytes(bytes: number): string {
  if (bytes >= 1_000_000) return `${(bytes / 1_000_000).toFixed(bytes >= 10_000_000 ? 0 : 1)} MB`;
  return `${Math.round(bytes / 1000)} KB`;
}

function renderPendingAttachments(): void {
  const container = byId("log-drawer-attachments");
  if (!container) return;
  clear(container);
  pendingAttachments.forEach((attachment) => {
    const remove = el("button", {
      class: "chat-attachment-remove",
      "data-attachment-id": attachment.id,
      title: "Remove",
      type: "button",
    }, ["x"]);
    remove.addEventListener("click", () => {
      pendingAttachments = pendingAttachments.filter((item) => item.id !== attachment.id);
      renderPendingAttachments();
    });
    container.append(el("div", { class: "chat-attachment-chip" }, [
      el("img", { alt: "", class: "chat-attachment-thumb", src: attachment.dataURL }),
      el("span", { class: "chat-attachment-name" }, [attachment.name]),
      remove,
    ]));
  });
}

function buildSubmitMessage(message: string, attachments: ChatAttachment[]): string {
  const parts = message ? [message] : [];
  void attachments;
  return parts.join("\n\n");
}

function appendDisplayTurns(container: Node, turns: DisplayTurn[]): number {
  for (const turn of turns) {
    container.appendChild(renderTurn(turn.role, turn.text, turn.timestamp));
  }
  return turns.length;
}

function expandTranscriptTurns(turns: TranscriptTurn[]): DisplayTurn[] {
  return turns.flatMap((turn) => expandTranscriptTurn(turn.role ?? "agent", turn.text ?? "", turn.timestamp));
}

function shouldReplaceWithStreamSnapshot(payload: StreamTurnPayload): boolean {
  const turns = payload.turns ?? [];
  return payload.format === "text" || turns.some((turn) => isTerminalTranscript(turn.role ?? "", turn.text ?? ""));
}

function replaceTranscriptTurns(turns: TranscriptTurn[]): void {
  const messagesEl = byId("log-drawer-messages");
  const loadingEl = byId("log-drawer-loading");
  if (!messagesEl || !loadingEl) return;
  const displayTurns = expandTranscriptTurns(turns);
  const fragment = document.createDocumentFragment();
  appendDisplayTurns(fragment, displayTurns);
  clear(messagesEl);
  messagesEl.append(fragment, loadingEl);
  loadingEl.style.display = "none";
  logCount = displayTurns.length;
  updateLogCount();
  scrollLogDrawerToBottom();
}

function updateLogCount(): void {
  byId("log-drawer-count")!.textContent = String(logCount);
}

function expandTranscriptTurn(role: string, text: string, timestamp: string | undefined): DisplayTurn[] {
  if (!isTerminalTranscript(role, text)) {
    return [{ role, text, timestamp }];
  }
  const parsed = parseCodexTerminalTranscript(text, timestamp);
  return parsed.length > 0 ? parsed : [{ role, text, timestamp }];
}

function isTerminalTranscript(role: string, text: string): boolean {
  if ((role ?? "").toLowerCase() !== "output") return false;
  return text.includes("\n› ") || text.startsWith("› ") || text.includes("\n• ") || text.startsWith("• ");
}

function parseCodexTerminalTranscript(text: string, timestamp: string | undefined): DisplayTurn[] {
  const turns: DisplayTurn[] = [];
  let current: { dropIfTerminalPrompt: boolean; role: string; lines: string[] } | null = null;

  const flush = (atEnd = false) => {
    if (!current) return;
    const body = trimBlankLines(current.lines).join("\n").trimEnd();
    if (body !== "" && !(atEnd && current.dropIfTerminalPrompt)) {
      turns.push({ role: current.role, text: body, timestamp });
    }
    current = null;
  };
  const startTurn = (role: string, firstLine: string) => {
    flush();
    current = { dropIfTerminalPrompt: false, role, lines: [firstLine] };
  };

  for (const rawLine of text.replace(/\r\n/g, "\n").split("\n")) {
    const line = rawLine.replace(/\s+$/g, "");
    if (isCodexSeparatorLine(line)) {
      flush();
      continue;
    }
    if (line.startsWith("› ")) {
      startTurn(roleForCodexPrompt(line.slice(2)), line.slice(2));
      continue;
    }
    if (line.startsWith("• ")) {
      startTurn("assistant", line.slice(2));
      continue;
    }
    if (isCodexStatusLine(line)) {
      if (current?.role === "user") current.dropIfTerminalPrompt = true;
      continue;
    }
    if (!current) {
      current = { dropIfTerminalPrompt: false, role: "system", lines: [] };
    }
    current.lines.push(line.startsWith("  ") ? line.slice(2) : line);
  }
  flush(true);
  return turns;
}

function roleForCodexPrompt(prompt: string): string {
  const trimmed = prompt.trim();
  if (
    trimmed.startsWith("<system-reminder>") ||
    /^\[[^\]]+\]\s+\S+\s+•/.test(trimmed) ||
    trimmed.startsWith("Stay idle.")
  ) {
    return "system";
  }
  return "user";
}

function isCodexSeparatorLine(line: string): boolean {
  return /^[─━═-]{20,}$/.test(line.trim());
}

function isCodexStatusLine(line: string): boolean {
  const trimmed = line.trim();
  return /^(gpt|claude|gemini|kimi|codex|openai)[\w.-]*(\s+\w+)*\s+·\s+/.test(trimmed);
}

function trimBlankLines(lines: string[]): string[] {
  let start = 0;
  let end = lines.length;
  while (start < end && lines[start]?.trim() === "") start += 1;
  while (end > start && lines[end - 1]?.trim() === "") end -= 1;
  return lines.slice(start, end);
}

function renderTurn(role: string, text: string, timestamp: string | undefined, localAttachments: ChatAttachment[] = []): HTMLElement {
  const className = roleClass(role);
  const parsed = extractInlineImageAttachments(text);
  const bodyText = parsed.text.trim();
  const attachments = [
    ...parsed.attachments,
    ...localAttachments.map((attachment) => ({ dataURL: attachment.dataURL, name: attachment.name })),
  ];
  return el("div", { class: `log-msg log-msg-${className}` }, [
    el("div", { class: "log-msg-header" }, [
      el("span", { class: `log-msg-type log-msg-type-${className}` }, [role]),
      el("span", { class: "log-msg-time" }, [formatTimestamp(timestamp)]),
    ]),
    bodyText ? el("div", { class: "log-msg-body" }, [bodyText]) : null,
    attachments.length > 0 ? el("div", { class: "log-msg-attachments" }, attachments.map((attachment) => (
      el("img", { alt: attachment.name, class: "log-msg-image", src: attachment.dataURL })
    ))) : null,
  ]);
}

function extractInlineImageAttachments(text: string): { attachments: Array<{ dataURL: string; name: string }>; text: string } {
  const attachments: Array<{ dataURL: string; name: string }> = [];
  let cleaned = text.replace(/!\[([^\]]*)\]\((data:image\/[^;)]+;base64,[A-Za-z0-9+/=\s]+)\)/g, (_match, name: string, dataURL: string) => {
    const normalizedDataURL = dataURL.replace(/\s+/g, "");
    if (normalizedDataURL.length > SESSION_SUBMIT_SAFE_BYTES) {
      return `[inline image data omitted: ${name || "image"}]`;
    }
    attachments.push({ dataURL: normalizedDataURL, name: name || "image" });
    return "";
  });
  cleaned = collapseLooseDataImagePayloads(cleaned);
  cleaned = collapseLooseBase64Payloads(cleaned);
  return { attachments, text: cleaned };
}

function collapseLooseDataImagePayloads(text: string): string {
  return text.replace(/data:image\/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=\s]{2000,}/g, "[inline image data omitted]");
}

function collapseLooseBase64Payloads(text: string): string {
  return text.replace(/[A-Za-z0-9+/=\r\n]{2000,}/g, (chunk) => {
    if (!looksLikeBase64Payload(chunk)) return chunk;
    return "[large encoded image data omitted from transcript]";
  });
}

function looksLikeBase64Payload(chunk: string): boolean {
  const compact = chunk.replace(/\s+/g, "");
  return compact.length > 1500 && /^[A-Za-z0-9+/=]+$/.test(compact) && /[+/]/.test(compact);
}

function scrollLogDrawerToBottom(): void {
  const body = byId("log-drawer-body");
  if (!body) return;
  window.requestAnimationFrame(() => {
    body.scrollTop = body.scrollHeight;
  });
}

function roleClass(role: string): string {
  switch ((role ?? "").toLowerCase()) {
    case "assistant":
    case "agent":
      return "assistant";
    case "system":
      return "system";
    case "output":
    case "result":
    case "tool":
    case "tool_result":
      return "result";
    default:
      return "user";
  }
}
