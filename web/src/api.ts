// These mirror the JSON in internal/api/views.go. Times are Unix seconds;
// zero times are left out.

export type SandboxState = "stopped" | "starting" | "working" | "needsYou" | "failed" | "ready" | "idle";

/** The TUI's groups, in the order it lists them. */
export const groups = ["needsYou", "working", "ready", "parked"] as const;
export type StatusGroup = (typeof groups)[number];

export const groupLabel: Record<StatusGroup, string> = {
  needsYou: "Needs you",
  working: "Working",
  ready: "Ready",
  parked: "Parked",
};

export function groupOf(state: SandboxState): StatusGroup {
  switch (state) {
    case "needsYou":
    case "failed":
      return "needsYou";
    case "working":
    case "starting":
      return "working";
    case "ready":
      return "ready";
    default:
      return "parked";
  }
}

export interface Project {
  id: number;
  name: string;
  path: string;
  remote?: string;
  defaultBranch: string;
  layout: string;
  kits: string[];
}

export interface Port {
  hostIP?: string;
  hostPort: number;
  sandboxPort: number;
  protocol: string;
}

export interface Sandbox {
  name: string;
  projectId: number;
  title: string;
  branch?: string;
  agent: string;
  agentLabel: string;
  model?: string;
  modelLabel?: string;
  clone: boolean;
  source: string;
  running: boolean;
  agentUp: boolean;
  state: SandboxState;
  statusText: string;
  busy: boolean;
  unread: boolean;
  hasReply: boolean;
  reportsActivity: boolean;
  canPR: boolean;
  canCheckout: boolean;
  added: number;
  removed: number;
  files: number;
  cpus?: number;
  memoryMiB?: number;
  ports: Port[];
  kits: string[];
  mcps: string[];
  createdAt: number;
  activeAt?: number;
}

export type FeedKind = "plain" | "you" | "good" | "warn" | "bad";

export interface SandboxDetail extends Sandbox {
  activity: { at: number; text: string; path?: string; kind: FeedKind }[];
  changes: { path: string; added: number; removed: number; binary?: boolean }[];
  commits: { hash: string; at: number; subject: string }[];
  base: string;
  workspace: string;
  probedAt?: number;
}

export interface Reply {
  at: number;
  markdown: string;
}

export interface Diff {
  files: { path: string; note?: string; lines: string[] }[];
  truncated?: boolean;
}

export interface Options {
  agents: { id: string; label: string; models: { id: string; label: string }[]; reportsActivity: boolean }[];
  mcpServers: { name: string; ready: boolean; status: string; detail?: string; nextStep?: string }[] | null;
  mcpError?: string;
  defaults: { agent: string; clone: boolean; kits: string[]; mcps: string[]; models: Record<string, string> };
}

export interface CreateRequest {
  projectId: number;
  agent: string;
  source: "branch" | "pr";
  value: string;
  task: string;
  clone: boolean;
  kits: string[];
  model: string;
  mcps: string[];
}

export interface NewProjectRequest {
  /** The project's folder, made in the Mac's projects folder. Left empty, a clone is named after its URL. */
  name: string;
  cloneUrl: string;
  /** Creates a private GitHub repo for a new project. */
  publish: boolean;
  /** When set, starts a first sandbox with it. */
  task: string;
}

export interface NewProjectResult {
  project: Project;
  sandbox?: Sandbox;
  /** A step that failed after the project was added, such as publishing it. */
  warning?: string;
}

export interface ProjectOptions {
  /** Where new projects go on the Mac. */
  dir: string;
  /** Whether gh is installed and signed in on the Mac. */
  canPublish: boolean;
}

export interface ServerInfo {
  id: string;
  name: string;
  version: number;
  pairing: boolean;
}

export interface ServerEvent {
  type: "snapshot" | "sandbox" | "removed" | "projects" | "status" | "action";
  projects?: Project[];
  sandboxes?: Sandbox[];
  listError?: string;
  sandbox?: Sandbox;
  name?: string;
  ok?: boolean;
  message?: string;
}

const terminalProtocol = "sbxp-terminal";

/** Close codes the terminal ends with; their reasons are written for people. */
export const terminalClose = { detached: 1000, unavailable: 4000, signedOut: 4001 } as const;

/** The API's version this app was written against. */
export const apiVersion = 1;

/** The Mac answered with an error, whose message is written for people. */
export class APIError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
  get signedOut() {
    return this.status === 401;
  }
}

const tokenKey = "sbxp.token";

export function savedToken(): string | null {
  try {
    return localStorage.getItem(tokenKey);
  } catch {
    return null;
  }
}

export function saveToken(token: string | null) {
  try {
    if (token) localStorage.setItem(tokenKey, token);
    else localStorage.removeItem(tokenKey);
  } catch {
    // Private browsing: the token lasts until the tab closes.
  }
}

async function request<T>(token: string | null, method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  let res: Response;
  try {
    res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal });
  } catch (e) {
    if (signal?.aborted) throw e;
    throw new APIError("Can't reach the Mac. Check that sbx-projects api is running.", 0);
  }
  const text = await res.text();
  const data = text ? JSON.parse(text) : undefined;
  if (!res.ok) throw new APIError(data?.error ?? `The Mac answered ${res.status}`, res.status);
  return data as T;
}

/** A client for one Mac's API, with the token this browser paired with. */
export class Client {
  constructor(readonly token: string) {}

  private call<T>(method: string, path: string, body?: unknown, signal?: AbortSignal) {
    return request<T>(this.token, method, path, body, signal);
  }

  detail = (name: string, signal?: AbortSignal) => this.call<SandboxDetail>("GET", `/v1/sandboxes/${enc(name)}`, undefined, signal);
  reply = (name: string) => this.call<Reply>("GET", `/v1/sandboxes/${enc(name)}/reply`);
  seen = (name: string) => this.call<Sandbox>("POST", `/v1/sandboxes/${enc(name)}/seen`);
  diff = (name: string) => this.call<Diff>("GET", `/v1/sandboxes/${enc(name)}/diff`);
  message = (name: string, text: string) => this.call<Sandbox>("POST", `/v1/sandboxes/${enc(name)}/messages`, { text });
  openPR = (name: string) => this.call<Sandbox>("POST", `/v1/sandboxes/${enc(name)}/pr`);
  stop = (name: string) => this.call<Sandbox>("POST", `/v1/sandboxes/${enc(name)}/stop`);
  resume = (name: string) => this.call<Sandbox>("POST", `/v1/sandboxes/${enc(name)}/resume`);
  checkout = (name: string) => this.call<Sandbox>("POST", `/v1/sandboxes/${enc(name)}/checkout`);
  /** Without a host port, the sandbox's port number is used on the Mac too. */
  publishPort = (name: string, p: { sandboxPort: number; hostPort?: number; hostIP?: string; protocol?: string }) =>
    this.call<Sandbox>("POST", `/v1/sandboxes/${enc(name)}/ports`, p);
  unpublishPort = (name: string, p: Port) => this.call<Sandbox>("POST", `/v1/sandboxes/${enc(name)}/ports/unpublish`, p);
  remove = (name: string) => this.call<Sandbox>("DELETE", `/v1/sandboxes/${enc(name)}`);
  create = (r: CreateRequest) => this.call<Sandbox>("POST", "/v1/sandboxes", r);
  options = (project: number) => this.call<Options>("GET", `/v1/options?project=${project}`);
  newProject = (r: NewProjectRequest) => this.call<NewProjectResult>("POST", "/v1/projects", r);
  projectOptions = () => this.call<ProjectOptions>("GET", "/v1/project-options");

  /**
   * Opens the agent's terminal. Browsers can't send headers on a WebSocket,
   * so the token goes as a second subprotocol. Output arrives as binary
   * frames; send keystrokes as binary frames and resizes as JSON text.
   */
  terminal(name: string, cols: number, rows: number): WebSocket {
    const scheme = location.protocol === "https:" ? "wss" : "ws";
    const url = `${scheme}://${location.host}/v1/sandboxes/${enc(name)}/terminal?cols=${cols}&rows=${rows}`;
    const ws = new WebSocket(url, [terminalProtocol, `bearer.${this.token}`]);
    ws.binaryType = "arraybuffer";
    return ws;
  }

  /**
   * Reads the event stream until it ends. EventSource can't send the token,
   * so this reads the response body; each event is one "data:" line.
   */
  async events(onEvent: (e: ServerEvent) => void, signal: AbortSignal): Promise<void> {
    let res: Response;
    try {
      res = await fetch("/v1/events", { headers: { Authorization: `Bearer ${this.token}` }, signal });
    } catch (e) {
      if (signal.aborted) throw e;
      throw new APIError("Can't reach the Mac. Check that sbx-projects api is running.", 0);
    }
    if (!res.ok || !res.body) {
      const data = await res.json().catch(() => undefined);
      throw new APIError(data?.error ?? `The Mac answered ${res.status}`, res.status);
    }
    const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
    let buf = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      buf += value;
      let nl: number;
      while ((nl = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, nl);
        buf = buf.slice(nl + 1);
        if (line.startsWith("data: ")) onEvent(JSON.parse(line.slice(6)));
      }
    }
  }
}

export const serverInfo = () => request<ServerInfo>(null, "GET", "/v1/server");

export const pair = (code: string, device: string) =>
  request<{ token: string; deviceId: string; server: { id: string; name: string } }>(null, "POST", "/v1/pair", { code, device });

const enc = encodeURIComponent;
