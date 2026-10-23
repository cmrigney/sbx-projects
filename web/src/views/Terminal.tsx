import { useEffect, useRef, useState } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal as XTerm } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { terminalClose, type Sandbox } from "../api";
import { useApp } from "../store";
import { Icon, back } from "../components";

type Phase = { kind: "connecting" } | { kind: "open" } | { kind: "closed"; message: string; again: boolean };

// The terminal is always dark, as the agents' own UIs expect, in the app's
// dark colours.
const theme = {
  background: "#0a141d",
  foreground: "#e4efec",
  cursor: "#85e8cd",
  cursorAccent: "#0a141d",
  selectionBackground: "rgba(133, 232, 205, 0.3)",
  black: "#101e2b",
  red: "#ff7b6e",
  green: "#6fdc9a",
  yellow: "#f5b94a",
  blue: "#7cc4ff",
  magenta: "#d3a6ff",
  cyan: "#85e8cd",
  white: "#e4efec",
  brightBlack: "#5f7787",
  brightRed: "#ff9d93",
  brightGreen: "#94e8b5",
  brightYellow: "#ffd07a",
  brightBlue: "#a5d6ff",
  brightMagenta: "#e2c2ff",
  brightCyan: "#b0f2df",
  brightWhite: "#ffffff",
};

// Keys a phone's keyboard doesn't have.
const keys: { label: string; aria: string; data: string }[] = [
  { label: "esc", aria: "Escape", data: "\x1b" },
  { label: "tab", aria: "Tab", data: "\t" },
  { label: "↑", aria: "Up", data: "\x1b[A" },
  { label: "↓", aria: "Down", data: "\x1b[B" },
  { label: "←", aria: "Left", data: "\x1b[D" },
  { label: "→", aria: "Right", data: "\x1b[C" },
  { label: "^C", aria: "Control C", data: "\x03" },
];

/**
 * The agent's session, as Enter attaches to it in the TUI. Leaving
 * detaches; the agent keeps running.
 */
export function TerminalView({ sandbox }: { sandbox: Sandbox }) {
  const { client } = useApp();
  const host = useRef<HTMLDivElement>(null);
  const term = useRef<XTerm | null>(null);
  const send = useRef<(data: string) => void>(() => {});
  const [phase, setPhase] = useState<Phase>({ kind: "connecting" });
  const [attempt, setAttempt] = useState(0);
  const [ctrl, setCtrl] = useState(false);
  const ctrlRef = useRef(false);
  ctrlRef.current = ctrl;
  const viewport = useVisualViewport();

  useEffect(() => {
    if (!client || !host.current) return;
    const t = new XTerm({
      theme,
      fontFamily: '"JetBrains Mono", ui-monospace, Menlo, monospace',
      fontSize: matchMedia("(max-width: 600px)").matches ? 12 : 14,
      cursorBlink: true,
      scrollback: 5000,
    });
    const fit = new FitAddon();
    t.loadAddon(fit);
    term.current = t;
    let ws: WebSocket | null = null;
    let disposed = false;
    const enc = new TextEncoder();

    send.current = (data) => ws?.readyState === WebSocket.OPEN && ws.send(enc.encode(data));
    t.onData((data) => {
      // The key bar's Ctrl applies to the next key typed.
      if (ctrlRef.current && data.length === 1) {
        const c = data.toUpperCase().charCodeAt(0);
        if (c >= 64 && c < 96) data = String.fromCharCode(c - 64);
        setCtrl(false);
      }
      send.current(data);
    });
    t.onBinary((data) => ws?.readyState === WebSocket.OPEN && ws.send(Uint8Array.from(data, (ch) => ch.charCodeAt(0))));
    t.onResize(({ cols, rows }) => ws?.readyState === WebSocket.OPEN && ws.send(JSON.stringify({ type: "resize", cols, rows })));

    // Measure with the font the terminal draws with.
    document.fonts.load('14px "JetBrains Mono"').finally(() => {
      if (disposed || !host.current) return;
      t.open(host.current);
      fit.fit();
      t.focus();
      ws = client.terminal(sandbox.name, t.cols, t.rows);
      ws.onopen = () => setPhase({ kind: "open" });
      ws.onmessage = (e) => t.write(new Uint8Array(e.data as ArrayBuffer));
      ws.onclose = (e) => {
        if (disposed) return;
        setPhase(closed(e));
      };
    });

    const refit = () => {
      if (t.element) fit.fit();
    };
    const ro = new ResizeObserver(refit);
    ro.observe(host.current);
    return () => {
      disposed = true;
      ro.disconnect();
      ws?.close();
      t.dispose();
      term.current = null;
    };
  }, [client, sandbox.name, attempt]);

  const again = () => {
    setPhase({ kind: "connecting" });
    setAttempt((n) => n + 1);
  };

  const press = (data: string) => {
    send.current(data);
    term.current?.focus();
  };

  return (
    <div className="term-page" style={viewport} role="dialog" aria-modal aria-label={`${sandbox.name} terminal`}>
      <header className="term-bar">
        <button className="ghost back-btn" onClick={() => back({ page: "sandbox", name: sandbox.name })}>
          <Icon name="back" size={18} />
          Detach
        </button>
        <span className="term-title">
          <span className={`term-dot ${phase.kind}`} />
          {sandbox.name}
        </span>
        <kbd className="hint">ctrl+b d</kbd>
      </header>
      <div className="term-body">
        <div className="term-host" ref={host} />
        {phase.kind === "closed" && (
          <div className="term-closed">
            <p>{phase.message}</p>
            <div className="button-row">
              {phase.again && (
                <button className="btn small primary" onClick={again}>
                  <Icon name="terminal" size={16} /> Attach again
                </button>
              )}
              <button className="btn small" onClick={() => back({ page: "sandbox", name: sandbox.name })}>
                Back to {sandbox.name}
              </button>
            </div>
          </div>
        )}
      </div>
      <div className="term-keys" role="toolbar" aria-label="Keys">
        <button className={ctrl ? "on" : ""} aria-pressed={ctrl} aria-label="Control" onPointerDown={(e) => e.preventDefault()} onClick={() => setCtrl((c) => !c)}>
          ctrl
        </button>
        {keys.map((k) => (
          // Keeping focus in the terminal keeps the phone's keyboard up.
          <button key={k.label} aria-label={k.aria} onPointerDown={(e) => e.preventDefault()} onClick={() => press(k.data)}>
            {k.label}
          </button>
        ))}
      </div>
    </div>
  );
}

function closed(e: CloseEvent): Phase {
  switch (e.code) {
    case terminalClose.detached:
      return { kind: "closed", message: e.reason || "Detached. The agent keeps running.", again: true };
    case terminalClose.unavailable:
      return { kind: "closed", message: e.reason, again: true };
    case terminalClose.signedOut:
      return { kind: "closed", message: e.reason, again: false };
    default:
      return { kind: "closed", message: "Lost the connection to the Mac.", again: true };
  }
}

/** The part of the page above a phone's on-screen keyboard. */
function useVisualViewport(): { top: number; height: number } | undefined {
  const read = () => {
    const vv = window.visualViewport;
    return vv ? { top: vv.offsetTop, height: vv.height } : undefined;
  };
  const [v, setV] = useState(read);
  useEffect(() => {
    const vv = window.visualViewport;
    if (!vv) return;
    const on = () => setV(read());
    vv.addEventListener("resize", on);
    vv.addEventListener("scroll", on);
    return () => {
      vv.removeEventListener("resize", on);
      vv.removeEventListener("scroll", on);
    };
  }, []);
  return v;
}
