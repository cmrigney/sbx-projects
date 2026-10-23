import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  APIError,
  Client,
  groupOf,
  groups,
  saveToken,
  savedToken,
  serverInfo,
  type CreateRequest,
  type Project,
  type Sandbox,
  type ServerEvent,
  type ServerInfo,
  type StatusGroup,
} from "./api";

export type Connection =
  | { kind: "connecting" }
  | { kind: "live" }
  | { kind: "offline"; message: string }
  | { kind: "signedOut"; message: string };

export interface Toast {
  id: number;
  message: string;
  ok: boolean;
}

interface AppState {
  server: ServerInfo | null;
  client: Client | null;
  connection: Connection;
  projects: Project[];
  sandboxes: Record<string, Sandbox>;
  listError?: string;
  toast: Toast | null;
  dismissToast: () => void;
  paired: (token: string) => void;
  signOut: () => void;
  /** Starts an action; how it ends arrives as a toast. Returns whether the Mac took it. */
  act: (name: string, fn: (c: Client) => Promise<Sandbox>) => Promise<boolean>;
  create: (r: CreateRequest) => Promise<Sandbox>;
  markSeen: (name: string) => Promise<void>;
  fail: (message: string) => void;
}

const Ctx = createContext<AppState | null>(null);

export function useApp(): AppState {
  const v = useContext(Ctx);
  if (!v) throw new Error("useApp outside AppProvider");
  return v;
}

export function AppProvider({ children }: { children: ReactNode }) {
  const [server, setServer] = useState<ServerInfo | null>(null);
  const [token, setToken] = useState<string | null>(savedToken);
  const [connection, setConnection] = useState<Connection>({ kind: "connecting" });
  const [projects, setProjects] = useState<Project[]>([]);
  const [sandboxes, setSandboxes] = useState<Record<string, Sandbox>>({});
  const [listError, setListError] = useState<string>();
  const [toast, setToast] = useState<Toast | null>(null);
  // Sandboxes whose action this browser started, so their outcome is shown.
  const pending = useRef(new Set<string>());
  // Outcomes no one here was waiting for. A new sandbox's name is known only
  // once creating it answers, which can be after its outcome arrives.
  const unclaimed = useRef(new Map<string, ServerEvent>());

  // Request responses are only a fallback until the stream updates that sandbox.
  // Keep removal revisions too, so late responses cannot resurrect deleted entries.
  const revision = useRef(0);
  const snapshotRevision = useRef(0);
  const sandboxRevisions = useRef(new Map<string, number>());
  const applyResponse = useCallback((s: Sandbox, started: number) => {
    if (snapshotRevision.current > started || (sandboxRevisions.current.get(s.name) ?? 0) > started) return;
    setSandboxes((all) => ({ ...all, [s.name]: s }));
  }, []);

  const client = useMemo(() => (token ? new Client(token) : null), [token]);

  const show = useCallback((message: string, ok: boolean) => setToast({ id: Date.now(), message, ok }), []);

  useEffect(() => {
    serverInfo().then(setServer, () => {});
  }, []);

  const apply = useCallback(
    (e: ServerEvent) => {
      switch (e.type) {
        case "snapshot":
          snapshotRevision.current = ++revision.current;
          sandboxRevisions.current.clear();
          setProjects(e.projects ?? []);
          setSandboxes(Object.fromEntries((e.sandboxes ?? []).map((s) => [s.name, s])));
          setListError(e.listError);
          setConnection({ kind: "live" });
          break;
        case "sandbox":
          if (e.sandbox) {
            const s = e.sandbox;
            sandboxRevisions.current.set(s.name, ++revision.current);
            setSandboxes((all) => ({ ...all, [s.name]: s }));
          }
          break;
        case "removed":
          if (e.name) {
            const name = e.name;
            sandboxRevisions.current.set(name, ++revision.current);
            setSandboxes(({ [name]: _, ...rest }) => rest);
          }
          break;
        case "projects":
          if (e.projects) setProjects(e.projects);
          break;
        case "status":
          // Until it clears, the states shown are the last ones seen.
          setListError(e.listError);
          break;
        case "action":
          if (!e.name) break;
          if (pending.current.delete(e.name) || !e.ok) show(e.message ?? "", !!e.ok);
          else unclaimed.current.set(e.name, e);
          break;
      }
    },
    [show],
  );

  // Keeps the event stream open while the page is visible, and reconnects
  // when it comes back.
  useEffect(() => {
    if (!client) return;
    let ctrl: AbortController | null = null;
    let timer: number | undefined;
    let failures = 0;
    let stopped = false;

    const run = async () => {
      if (stopped || document.hidden) return;
      ctrl = new AbortController();
      setConnection((c) => (c.kind === "live" ? c : { kind: "connecting" }));
      try {
        await client.events((e) => {
          failures = 0;
          apply(e);
        }, ctrl.signal);
        failures = 0;
      } catch (e) {
        if (ctrl.signal.aborted) return;
        if (e instanceof APIError && e.signedOut) {
          setConnection({ kind: "signedOut", message: e.message });
          return;
        }
        failures++;
        setConnection({ kind: "offline", message: e instanceof Error ? e.message : String(e) });
      }
      if (!stopped) timer = window.setTimeout(run, Math.min(2000 * Math.max(failures, 1), 10000));
    };

    const onVisibility = () => {
      clearTimeout(timer);
      ctrl?.abort();
      if (!document.hidden) run();
    };
    document.addEventListener("visibilitychange", onVisibility);
    run();
    return () => {
      stopped = true;
      clearTimeout(timer);
      ctrl?.abort();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [client, apply]);

  const paired = useCallback((t: string) => {
    saveToken(t);
    setToken(t);
    setConnection({ kind: "connecting" });
    serverInfo().then(setServer, () => {});
  }, []);

  const signOut = useCallback(() => {
    snapshotRevision.current = ++revision.current;
    sandboxRevisions.current.clear();
    saveToken(null);
    setToken(null);
    setSandboxes({});
    setProjects([]);
  }, []);

  const act = useCallback(
    async (name: string, fn: (c: Client) => Promise<Sandbox>) => {
      if (!client) return false;
      // Before the request, as the outcome can arrive before its answer.
      pending.current.add(name);
      const started = revision.current;
      try {
        const s = await fn(client);
        applyResponse(s, started);
        return true;
      } catch (e) {
        pending.current.delete(name);
        show(e instanceof Error ? e.message : String(e), false);
        return false;
      }
    },
    [client, show, applyResponse],
  );

  const create = useCallback(
    async (r: CreateRequest) => {
      if (!client) throw new Error("Not paired");
      unclaimed.current.clear();
      const started = revision.current;
      const s = await client.create(r);
      const done = unclaimed.current.get(s.name);
      if (done) show(done.message ?? "", !!done.ok);
      else pending.current.add(s.name);
      applyResponse(s, started);
      return s;
    },
    [client, show, applyResponse],
  );

  const markSeen = useCallback(
    async (name: string) => {
      if (!client) return;
      const started = revision.current;
      try {
        const s = await client.seen(name);
        applyResponse(s, started);
      } catch {
        // The mark stays; the next reading clears it.
      }
    },
    [client, applyResponse],
  );

  const value: AppState = {
    server,
    client,
    connection,
    projects,
    sandboxes,
    listError,
    toast,
    dismissToast: () => setToast(null),
    paired,
    signOut,
    act,
    create,
    markSeen,
    fail: (m) => show(m, false),
  };
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

/** Whether a filter shows a sandbox. Null shows every sandbox but the parked ones. */
export function shows(group: StatusGroup | null, s: Sandbox): boolean {
  return group === null ? groupOf(s.state) !== "parked" : groupOf(s.state) === group;
}

/** The project's sandboxes in the TUI's order: by group, then newest. */
export function sandboxesIn(all: Record<string, Sandbox>, project: Project, group: StatusGroup | null, query: string): Sandbox[] {
  const q = query.trim().toLowerCase();
  return Object.values(all)
    .filter((s) => s.projectId === project.id)
    .filter((s) => shows(group, s))
    .filter(
      (s) =>
        !q ||
        s.title.toLowerCase().includes(q) ||
        s.name.toLowerCase().includes(q) ||
        (s.branch ?? "").toLowerCase().includes(q),
    )
    .sort((a, b) => {
      const ga = groups.indexOf(groupOf(a.state));
      const gb = groups.indexOf(groupOf(b.state));
      if (ga !== gb) return ga - gb;
      if (a.createdAt !== b.createdAt) return b.createdAt - a.createdAt;
      return a.name.localeCompare(b.name);
    });
}
