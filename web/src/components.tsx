import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { groupLabel, groupOf, type FeedKind, type Sandbox, type SandboxState } from "./api";
import { useApp } from "./store";

// Routes live in the hash, so the browser's back button closes sheets and
// leaves a sandbox as the iPhone's back gesture does.

/** What opens over a sandbox. The terminal covers the whole app. */
export type Sheet = "reply" | "diff" | "remove" | "terminal";

export type Route =
  | { page: "list" }
  | { page: "sandbox"; name: string; sheet?: Sheet; file?: string }
  | { page: "new"; project?: number }
  | { page: "new-project" };

function parse(hash: string): Route {
  const [path, query] = hash.replace(/^#/, "").split("?");
  const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  if (parts[0] === "new") {
    const project = Number(new URLSearchParams(query).get("project"));
    return project ? { page: "new", project } : { page: "new" };
  }
  if (parts[0] === "new-project") return { page: "new-project" };
  if (parts[0] === "s" && parts[1]) {
    const sheet = parts[2] as Sheet | undefined;
    const file = new URLSearchParams(query).get("file") ?? undefined;
    return { page: "sandbox", name: parts[1], sheet, file };
  }
  return { page: "list" };
}

export function href(r: Route): string {
  switch (r.page) {
    case "list":
      return "#/";
    case "new":
      return r.project ? `#/new?project=${r.project}` : "#/new";
    case "new-project":
      return "#/new-project";
    case "sandbox": {
      let h = `#/s/${encodeURIComponent(r.name)}`;
      if (r.sheet) h += `/${r.sheet}`;
      if (r.file) h += `?file=${encodeURIComponent(r.file)}`;
      return h;
    }
  }
}

const subscribe = (fn: () => void) => {
  window.addEventListener("hashchange", fn);
  return () => window.removeEventListener("hashchange", fn);
};

export function useRoute(): Route {
  const hash = useSyncExternalStore(subscribe, () => location.hash);
  return parse(hash);
}

// Whether this tab has moved within the app, so going back stays in it
// rather than leaving a page that was opened at a sandbox's link.
let moved = false;
window.addEventListener("hashchange", () => (moved = true));

export function go(r: Route, replace = false) {
  if (replace) location.replace(href(r));
  else location.hash = href(r);
}

/** Closes a sheet or page by going back when the app opened it. */
export function back(fallback: Route) {
  if (moved) history.back();
  else go(fallback, true);
}

export const stateColor: Record<SandboxState, string> = {
  needsYou: "var(--s-needs)",
  failed: "var(--s-fail)",
  working: "var(--s-work)",
  starting: "var(--s-work)",
  ready: "var(--s-ready)",
  idle: "var(--s-parked)",
  stopped: "var(--s-parked)",
};

const stateWord: Record<SandboxState, string> = {
  needsYou: "needs you",
  failed: "failed",
  working: "working",
  starting: "starting",
  ready: "ready",
  idle: "idle",
  stopped: "stopped",
};

/**
 * A sandbox's state as a tinted square, like the sandbox in the app icon.
 * A working agent shows three pulsing dots.
 */
export function StatusMark({ state, size = 26 }: { state: SandboxState; size?: number }) {
  return (
    <span className={`mark mark-${state}`} style={{ width: size, height: size, color: stateColor[state] }} role="img" aria-label={groupLabel[groupOf(state)]}>
      {state === "working" || state === "starting" ? (
        <span className="dots" style={{ gap: Math.max(1, Math.round(size * 0.08)) }}>
          {[0, 1, 2].map((i) => (
            <span key={i} style={{ width: Math.max(2, Math.round(size * 0.14)), height: Math.max(2, Math.round(size * 0.14)) }} />
          ))}
        </span>
      ) : (
        <svg viewBox="0 0 24 24" width={size * 0.6} height={size * 0.6} aria-hidden>
          {glyph(state)}
        </svg>
      )}
    </span>
  );
}

/** The state in words, as a terminal would print it: "▍working". */
export function StatusTag({ state }: { state: SandboxState }) {
  return (
    <span className="status-tag" style={{ color: stateColor[state] }}>
      <StatusMark state={state} size={16} />
      {stateWord[state]}
    </span>
  );
}

/** An activity line's kind, drawn like the status marks. */
export function FeedGlyph({ kind }: { kind: FeedKind }) {
  return (
    <svg viewBox="0 0 24 24" width={13} height={13} aria-hidden>
      {kind === "you" ? <path d="m8 6 6 6-6 6" fill="none" stroke="currentColor" strokeWidth={2.8} strokeLinecap="round" strokeLinejoin="round" /> : glyph(feedState[kind])}
    </svg>
  );
}

const feedState: Record<Exclude<FeedKind, "you">, SandboxState> = { plain: "idle", good: "ready", warn: "needsYou", bad: "failed" };

function glyph(state: SandboxState) {
  const stroke = { fill: "none", stroke: "currentColor", strokeWidth: 2.6, strokeLinecap: "round", strokeLinejoin: "round" } as const;
  switch (state) {
    case "needsYou":
      return (
        <>
          <path d="M8.8 9a3.2 3.2 0 1 1 4.6 2.9c-.8.4-1.4 1.1-1.4 2v.6" {...stroke} />
          <circle cx="12" cy="18.4" r="1.5" fill="currentColor" />
        </>
      );
    case "ready":
      return <path d="m6 12.5 4 4 8-9" {...stroke} />;
    case "failed":
      return <path d="m7.5 7.5 9 9m0-9-9 9" {...stroke} />;
    case "stopped":
      return <path d="M9.5 7v10m5-10v10" {...stroke} />;
    default:
      return <circle cx="12" cy="12" r="2.6" fill="currentColor" />;
  }
}

/** Lines added and removed, in green and red. */
export function ChangeCounts({ added, removed }: { added: number; removed: number }) {
  return (
    <span className="counts">
      <span className="add">+{added}</span> <span className="del">−{removed}</span>
    </span>
  );
}

/** Five blocks split by added and removed lines, as git's --stat draws them. */
export function DiffStat({ added, removed }: { added: number; removed: number }) {
  const total = added + removed;
  const adds = total === 0 ? 0 : Math.round((5 * added) / total);
  return (
    <span className="diffstat" aria-hidden>
      {[0, 1, 2, 3, 4].map((i) => (
        <span key={i} className={total === 0 ? "" : i < adds ? "add" : "del"} />
      ))}
    </span>
  );
}

/** "now", "4m", "3h", "2d": how long ago, as short as the TUI shows it. */
export function shortAgo(at: number, now = Date.now() / 1000): string {
  const s = Math.floor(now - at);
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

/** Re-renders every half minute so "4m ago" keeps up. */
export function useTick() {
  const [, set] = useState(0);
  useEffect(() => {
    const t = setInterval(() => set((n) => n + 1), 30_000);
    return () => clearInterval(t);
  }, []);
}

/** A gigabyte figure for a MiB count: 8192 → "8 GB". */
export function memoryLabel(mib: number): string {
  const gb = mib / 1024;
  return Math.round(gb) === gb ? `${gb} GB` : `${gb.toFixed(1)} GB`;
}

export function Icon({ name, size = 22 }: { name: keyof typeof icons; size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden className="icon">
      {icons[name]}
    </svg>
  );
}

const line = { fill: "none", stroke: "currentColor", strokeWidth: 2, strokeLinecap: "round", strokeLinejoin: "round" } as const;

const icons = {
  plus: <path d="M12 5v14M5 12h14" {...line} />,
  back: <path d="m15 5-7 7 7 7" {...line} />,
  more: (
    <>
      <circle cx="5.5" cy="12" r="1.6" fill="currentColor" />
      <circle cx="12" cy="12" r="1.6" fill="currentColor" />
      <circle cx="18.5" cy="12" r="1.6" fill="currentColor" />
    </>
  ),
  send: <path d="M12 19V5m-6 6 6-6 6 6" {...line} strokeWidth={2.6} />,
  search: (
    <>
      <circle cx="11" cy="11" r="6.5" {...line} />
      <path d="m20 20-4.4-4.4" {...line} />
    </>
  ),
  close: <path d="M6 6l12 12M18 6 6 18" {...line} />,
  bubble: <path d="M5 5h14a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1h-8l-4 3v-3H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1z" {...line} />,
  pr: (
    <>
      <circle cx="6" cy="6" r="2.2" {...line} />
      <circle cx="6" cy="18" r="2.2" {...line} />
      <circle cx="18" cy="18" r="2.2" {...line} />
      <path d="M6 8.2v7.6M18 15.8V9a3 3 0 0 0-3-3h-4m2-2.5L10.5 6 13 8.5" {...line} />
    </>
  ),
  laptop: <path d="M5 6h14v9H5zM3 18h18M12 9v4m-2-2 2 2 2-2" {...line} />,
  stop: (
    <>
      <circle cx="12" cy="12" r="8.5" {...line} />
      <rect x="9" y="9" width="6" height="6" rx="1" fill="currentColor" />
    </>
  ),
  play: (
    <>
      <circle cx="12" cy="12" r="8.5" {...line} />
      <path d="M10 8.5v7l5.5-3.5z" fill="currentColor" />
    </>
  ),
  trash: <path d="M5 7h14M10 7V5h4v2m-7 0 1 12h8l1-12" {...line} />,
  copy: <path d="M9 9h10v10H9zM5 15V5h10" {...line} />,
  wifi: <path d="M2.5 9a14 14 0 0 1 19 0M5.8 12.4a9 9 0 0 1 12.4 0M9 15.7a4.5 4.5 0 0 1 6 0M12 19h.01" {...line} />,
  warn: <path d="M12 4 2.8 19.5h18.4zM12 10v4.5m0 2.5h.01" {...line} />,
  lock: <path d="M7 11V8a5 5 0 0 1 10 0v3M5.5 11h13v9h-13z" {...line} />,
  terminal: <path d="M4 5h16v14H4zM8 10l3 2.5L8 15m5 0h3" {...line} />,
  folderPlus: <path d="M3.5 6h6l2 2h9v11h-17zM12 11v5.5M9.25 13.75h5.5" {...line} />,
  // The sandbox from the app icon.
  box: (
    <>
      <path d="m4 9 7.6-4.2a1 1 0 0 1 .8 0L20 9z" fill="currentColor" opacity="0.55" />
      <path d="M4 9h16v9.5a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18.5z" fill="currentColor" />
    </>
  ),
  branch: (
    <>
      <circle cx="7" cy="6" r="2" {...line} />
      <circle cx="7" cy="18" r="2" {...line} />
      <circle cx="17" cy="8" r="2" {...line} />
      <path d="M7 8v8m10-6c0 4-6 3-9.2 6.4" {...line} />
    </>
  ),
  prompt: <path d="m5 7 5 5-5 5m7 0h7" {...line} strokeWidth={2.4} />,
};

/**
 * A sheet that rises from the bottom on a phone and sits centered on a
 * wide screen. Escape and the backdrop close it.
 */
export function Sheet({
  onClose,
  title,
  subtitle,
  leading,
  trailing,
  footer,
  children,
  label,
}: {
  onClose: () => void;
  title: ReactNode;
  subtitle?: ReactNode;
  leading?: ReactNode;
  trailing?: ReactNode;
  footer?: ReactNode;
  children: ReactNode;
  label: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  useEffect(() => {
    close.current = onClose;
  }, [onClose]);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    ref.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && close.current();
    window.addEventListener("keydown", onKey);
    document.body.classList.add("sheet-open");
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.classList.remove("sheet-open");
      prev?.focus?.();
    };
  }, []);
  return (
    <div className="backdrop" onClick={onClose}>
      <div className="sheet" role="dialog" aria-modal aria-label={label} ref={ref} tabIndex={-1} onClick={(e) => e.stopPropagation()}>
        <div className="grabber" aria-hidden />
        <header className="sheet-bar">
          <div className="sheet-title">
            <strong>{title}</strong>
            {subtitle && <small>{subtitle}</small>}
          </div>
          <div className="sheet-side">
            {leading}
            {trailing}
          </div>
        </header>
        <div className="sheet-body">{children}</div>
        {footer}
      </div>
    </div>
  );
}

/**
 * Types a message into the agent's session. A stopped sandbox is started,
 * and an agent that isn't running is started with the message.
 */
export function Composer({ sandbox, placeholder, onSent }: { sandbox: Sandbox; placeholder?: string; onSent?: () => void }) {
  const { act } = useApp();
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const ref = useRef<HTMLTextAreaElement>(null);
  const trimmed = text.trim();

  // Grows with what's typed, up to six lines.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 140)}px`;
  }, [text]);

  const send = async () => {
    if (!trimmed || sending || sandbox.busy) return;
    setSending(true);
    if (await act(sandbox.name, (c) => c.message(sandbox.name, trimmed))) {
      setText("");
      onSent?.();
    }
    setSending(false);
  };

  return (
    <form
      className="composer"
      onSubmit={(e) => {
        e.preventDefault();
        send();
      }}
    >
      <span className="composer-prompt" aria-hidden>
        <Icon name="prompt" size={16} />
      </span>
      <textarea
        id="composer"
        ref={ref}
        rows={1}
        value={text}
        placeholder={placeholder ?? `Message ${sandbox.agentLabel}`}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          // Enter sends on a keyboard; on a phone the return key adds a line.
          if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing && matchMedia("(pointer: fine)").matches) {
            e.preventDefault();
            send();
          }
        }}
        aria-label={placeholder ?? `Message ${sandbox.agentLabel}`}
      />
      <kbd className="hint composer-hint">⏎</kbd>
      <button type="submit" className="send" disabled={!trimmed || sending || sandbox.busy} aria-label="Send">
        {sending ? <span className="spinner small" /> : <Icon name="send" size={18} />}
      </button>
    </form>
  );
}

export function Toasts() {
  const { toast, dismissToast } = useApp();
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(dismissToast, toast.ok ? 3500 : 6000);
    return () => clearTimeout(t);
  }, [toast, dismissToast]);
  if (!toast) return null;
  return (
    <div className={`toast ${toast.ok ? "ok" : "bad"}`} role="status" key={toast.id} onClick={dismissToast}>
      {toast.message}
    </div>
  );
}

export function Empty({ icon, title, children }: { icon: keyof typeof icons; title: string; children?: ReactNode }) {
  return (
    <div className="empty">
      <span className="empty-mark">
        <Icon name={icon} size={26} />
      </span>
      <h3>{title}</h3>
      {children && <p>{children}</p>}
    </div>
  );
}

/**
 * Runs the TUI's single-key shortcuts while nothing is being typed into
 * and no sheet is open.
 */
export function useKeys(keys: Record<string, () => void>, enabled = true) {
  const ref = useRef(keys);
  ref.current = keys;
  useEffect(() => {
    if (!enabled) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return;
      const t = e.target as HTMLElement;
      if (t.closest("input, textarea, select, [contenteditable]") || document.querySelector(".backdrop, .term-page")) return;
      // Enter on a focused button or link is that button's.
      if (e.key === "Enter" && t.closest("button, a")) return;
      const fn = ref.current[e.key];
      if (fn) {
        e.preventDefault();
        fn();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [enabled]);
}
