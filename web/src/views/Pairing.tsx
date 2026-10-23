import { useEffect, useRef, useState } from "react";
import { apiVersion, pair, serverInfo, type ServerInfo } from "../api";
import { useApp } from "../store";

/** Trades the 8-character code `sbx-projects api --pair` shows for a token. */
export function Pairing() {
  const { paired } = useApp();
  const [info, setInfo] = useState<ServerInfo | null>(null);
  const [infoError, setInfoError] = useState<string>();
  const [code, setCode] = useState(() => new URLSearchParams(location.hash.split("?")[1]).get("code") ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    serverInfo().then(setInfo, (e) => setInfoError(e.message));
    input.current?.focus();
  }, []);

  const clean = code.toUpperCase().replace(/[^A-Z0-9]/g, "");
  const tooNew = info && info.version > apiVersion;

  const submit = async () => {
    if (clean.length !== 8 || busy) return;
    setBusy(true);
    setError(undefined);
    try {
      const r = await pair(clean, deviceName());
      location.hash = "#/";
      paired(r.token);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <div className="pairing">
      <img src="/icon.svg" alt="" width={72} height={72} className="app-icon" />
      <p className="eyebrow">sbx-projects</p>
      <h1>Pair this browser with {info?.name ?? "your Mac"}</h1>
      <div className="terminal" aria-label="On the Mac, run">
        <div className="terminal-bar" aria-hidden>
          <i />
          <i />
          <i />
          <span>on your Mac</span>
        </div>
        <code>
          <span className="ps1">$</span> sbx-projects api --pair
        </code>
      </div>
      <p className="muted">
        Enter the code it shows. This browser gets its own token; <code>api devices rm</code> signs it out.
      </p>
      {infoError && <p className="red">{infoError}</p>}
      {info && !info.pairing && <p className="orange">Pairing isn't open on {info.name}. Run it with --pair to get a code.</p>}
      {tooNew && <p className="orange">{info.name} runs a newer API than this page knows. Reload to get the latest page.</p>}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <input
          ref={input}
          className="code"
          value={clean.length > 4 ? `${clean.slice(0, 4)}-${clean.slice(4)}` : clean}
          onChange={(e) => setCode(e.target.value)}
          placeholder="XXXX-XXXX"
          autoCapitalize="characters"
          autoComplete="one-time-code"
          autoCorrect="off"
          spellCheck={false}
          maxLength={9}
          aria-label="Pairing code"
        />
        {error && <p className="red">{error}</p>}
        <button className="btn primary wide" disabled={clean.length !== 8 || busy}>
          {busy ? <span className="spinner small" /> : "Pair"}
        </button>
      </form>
    </div>
  );
}

/** What `api devices` lists this browser as: "Safari on iPhone". */
function deviceName(): string {
  const ua = navigator.userAgent;
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser";
  const os = /iPhone/.test(ua)
    ? "iPhone"
    : /iPad/.test(ua)
      ? "iPad"
      : /Android/.test(ua)
        ? "Android"
        : /Mac OS X/.test(ua)
          ? "Mac"
          : /Windows/.test(ua)
            ? "Windows"
            : /Linux/.test(ua)
              ? "Linux"
              : "the web";
  return `${browser} on ${os}`;
}
