import { useEffect, useRef, useState } from "react";
import type { Port, Sandbox, SandboxDetail as Detail } from "../api";
import { useApp } from "../store";
import { ChangeCounts, Composer, DiffStat, Empty, FeedGlyph, Icon, StatusTag, back, go, href, memoryLabel, shortAgo, stateColor, useKeys, useTick } from "../components";

type Tab = "activity" | "changes" | "setup";

export function SandboxDetail({ name, wide }: { name: string; wide: boolean }) {
  const { sandboxes, client, act } = useApp();
  const [detail, setDetail] = useState<Detail | null>(null);
  const [loadError, setLoadError] = useState<string>();
  const [tab, setTab] = useState<Tab>("activity");
  useTick();

  // The list's view is the freshest; the detail fills in the rest.
  const live = sandboxes[name];
  const s: Sandbox | undefined = live ?? detail ?? undefined;

  // Every change to the sandbox reloads its tabs.
  useEffect(() => {
    if (!client) return;
    const ctrl = new AbortController();
    client.detail(name, ctrl.signal).then(
      (d) => {
        setDetail(d);
        setLoadError(undefined);
      },
      (e) => !ctrl.signal.aborted && setLoadError(e.message),
    );
    return () => ctrl.abort();
  }, [client, name, live]);

  // Leaves a sandbox that was removed while it was open.
  const seen = useRef(false);
  useEffect(() => {
    if (live) seen.current = true;
    else if (seen.current) go({ page: "list" }, true);
  }, [live]);

  useEffect(() => setTab("activity"), [name]);

  // The TUI's keys, on a keyboard.
  const run = (ok: boolean | undefined, fn: () => void) => () => s && !s.busy && ok && fn();
  useKeys(
    {
      "1": () => setTab("activity"),
      "2": () => setTab("changes"),
      "3": () => setTab("setup"),
      Enter: run(true, () => go({ page: "sandbox", name, sheet: "terminal" })),
      i: () => document.getElementById("composer")?.focus(),
      t: run(s?.hasReply, () => go({ page: "sandbox", name, sheet: "reply" })),
      p: run(s?.canPR, () => act(name, (c) => c.openPR(name))),
      c: run(s?.canCheckout, () => act(name, (c) => c.checkout(name))),
      s: run(s?.running, () => act(name, (c) => c.stop(name))),
      r: run(s && !s.running, () => act(name, (c) => c.resume(name))),
      x: run(true, () => go({ page: "sandbox", name, sheet: "remove" })),
      Escape: () => !wide && back({ page: "list" }),
    },
    !!s,
  );

  return (
    <div className="page detail-page">
      <header className="bar detail-bar">
        {!wide ? (
          <button className="ghost back-btn" aria-label="Sandboxes" onClick={() => back({ page: "list" })}>
            <Icon name="back" size={18} />
            Sandboxes
          </button>
        ) : (
          <span className="bar-name">{s?.name ?? name}</span>
        )}
        {s && (
          <div className="bar-actions">
            <a
              className={`btn small attach-btn ${s.busy ? "disabled" : ""}`}
              href={href({ page: "sandbox", name, sheet: "terminal" })}
              aria-disabled={s.busy}
              title="Attach to the agent's terminal"
            >
              <Icon name="terminal" size={16} />
              Attach
              <kbd className="hint">enter</kbd>
            </a>
            <Actions s={s} />
          </div>
        )}
      </header>
      {!s && loadError && (
        <Empty icon="warn" title={`Couldn't load ${name}`}>
          {loadError}
        </Empty>
      )}
      {!s && !loadError && <div className="loading"><span className="spinner" /></div>}
      {s && (
        <>
          <div className="detail-scroll">
            <Header s={s} />
            {s.state === "needsYou" ? (
              <div className="callout ask">
                <div className="callout-head">
                  <span>
                    <Icon name="prompt" size={14} /> {s.agentLabel} is asking
                  </span>
                  {s.activeAt ? <span>{shortAgo(s.activeAt)} ago</span> : null}
                </div>
                <p>{s.statusText}</p>
                <button className="link" onClick={() => document.getElementById("composer")?.focus()}>
                  Answer below
                </button>
              </div>
            ) : s.unread && s.hasReply ? (
              <a className="callout reply" href={href({ page: "sandbox", name, sheet: "reply" })}>
                <div className="callout-head">
                  <span>
                    <Icon name="bubble" size={14} /> New reply
                  </span>
                </div>
                <p className="clamp3">{s.statusText}</p>
                <span className="link">Read it →</span>
              </a>
            ) : null}
            <div className="tabs" role="tablist">
              {(["activity", "changes", "setup"] as Tab[]).map((t, i) => (
                <button key={t} role="tab" aria-selected={tab === t} className={tab === t ? "on" : ""} onClick={() => setTab(t)}>
                  {t[0].toUpperCase() + t.slice(1)}
                  {t === "activity" && detail ? <span className="n">{detail.activity.length}</span> : null}
                  {t === "changes" && s.files > 0 ? <span className="n">{s.files}</span> : null}
                  <kbd className="hint">{i + 1}</kbd>
                </button>
              ))}
            </div>
            {tab === "activity" && <ActivityTab s={s} detail={detail} />}
            {tab === "changes" && <ChangesTab s={s} detail={detail} />}
            {tab === "setup" && <SetupTab s={s} detail={detail} />}
          </div>
          <div className="composer-dock">
            <Composer sandbox={s} />
          </div>
        </>
      )}
    </div>
  );
}

function Header({ s }: { s: Sandbox }) {
  const tags = [s.modelLabel ? `${s.agentLabel} · ${s.modelLabel}` : s.agentLabel];
  if (s.cpus && s.memoryMiB) tags.push(`${s.cpus} cpu · ${memoryLabel(s.memoryMiB)}`);
  tags.push(s.clone ? "private clone" : "your working tree");
  return (
    <div className="detail-head" style={{ "--rail": stateColor[s.state] } as React.CSSProperties}>
      <div className="detail-status">
        <StatusTag state={s.state} />
        {s.activeAt ? <span className="ago">{shortAgo(s.activeAt)}</span> : null}
      </div>
      <h1>{s.title}</h1>
      {s.branch && (
        <span className="branch">
          <Icon name="branch" size={14} />
          {s.branch}
        </span>
      )}
      {s.state !== "needsYou" && <p className={`detail-text ${s.state === "failed" ? "loud" : ""}`}>{s.statusText}</p>}
      <div className="tags">
        {tags.map((t) => (
          <span key={t} className="tag">{t}</span>
        ))}
      </div>
    </div>
  );
}

/** The TUI's sandbox actions, behind the ⋯ button. */
function Actions({ s }: { s: Sandbox }) {
  const { act } = useApp();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (e: Event) => !ref.current?.contains(e.target as Node) && setOpen(false);
    const key = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", key);
    };
  }, [open]);

  const item = (label: string, icon: Parameters<typeof Icon>[0]["name"], key: string, run: () => void, disabled = false, danger = false) => (
    <button
      role="menuitem"
      className={danger ? "danger" : ""}
      disabled={disabled}
      onClick={() => {
        setOpen(false);
        run();
      }}
    >
      <Icon name={icon} size={17} />
      <span>{label}</span>
      <kbd className="hint">{key}</kbd>
    </button>
  );

  return (
    <div className="menu-wrap" ref={ref}>
      <button className="ghost icon-btn" aria-label="Actions" aria-haspopup="menu" aria-expanded={open} disabled={s.busy} onClick={() => setOpen((o) => !o)}>
        {s.busy ? <span className="spinner small" /> : <Icon name="more" />}
      </button>
      {open && (
        <div className="menu" role="menu">
          {item("Attach", "terminal", "enter", () => go({ page: "sandbox", name: s.name, sheet: "terminal" }))}
          {item("Last reply", "bubble", "t", () => go({ page: "sandbox", name: s.name, sheet: "reply" }), !s.hasReply)}
          {item("Open draft PR", "pr", "p", () => act(s.name, (c) => c.openPR(s.name)), !s.canPR)}
          {item("Check out on Mac", "laptop", "c", () => act(s.name, (c) => c.checkout(s.name)), !s.canCheckout)}
          <hr />
          {s.running
            ? item("Stop", "stop", "s", () => act(s.name, (c) => c.stop(s.name)))
            : item("Resume", "play", "r", () => act(s.name, (c) => c.resume(s.name)))}
          {item("Remove…", "trash", "x", () => go({ page: "sandbox", name: s.name, sheet: "remove" }), false, true)}
        </div>
      )}
    </div>
  );
}

function ActivityTab({ s, detail }: { s: Sandbox; detail: Detail | null }) {
  if (!detail) return <div className="loading small"><span className="spinner" /></div>;
  if (detail.activity.length === 0) {
    return (
      <p className="note center">
        {s.reportsActivity ? "No activity yet. It appears once the agent starts working." : `${s.agentLabel} doesn't report what it is doing.`}
      </p>
    );
  }
  return (
    <>
      <ol className="panel log">
        {detail.activity.map((f, i) => (
          <li key={i} className={`log-${f.kind}`}>
            <span className="log-at">{shortAgo(f.at)}</span>
            <span className="log-glyph">
              <FeedGlyph kind={f.kind} />
            </span>
            <span className="log-text">
              {f.text}
              {f.path && <> <code>{f.path}</code></>}
            </span>
          </li>
        ))}
      </ol>
      {detail.probedAt ? <p className="note">Last seen {relative(detail.probedAt)}, while the sandbox was running.</p> : null}
    </>
  );
}

function ChangesTab({ s, detail }: { s: Sandbox; detail: Detail | null }) {
  const { act } = useApp();
  if (!detail) return <div className="loading small"><span className="spinner" /></div>;
  // A stopped private clone has no working tree to diff.
  const noDiff = !s.running && s.clone;
  return (
    <>
      {detail.changes.length === 0 ? (
        <p className="note center">
          No changes against <code>{detail.base}</code> yet.
        </p>
      ) : (
        <>
          <div className="section-head">
            <span>
              {detail.changes.length} {detail.changes.length === 1 ? "file" : "files"} against <code>{detail.base}</code>
            </span>
            <ChangeCounts added={s.added} removed={s.removed} />
          </div>
          <ul className="panel files">
            {detail.changes.map((f) => {
              const i = f.path.lastIndexOf("/");
              const body = (
                <>
                  <code className="file-path">
                    <span className="dim">{f.path.slice(0, i + 1)}</span>
                    {f.path.slice(i + 1)}
                  </code>
                  {f.binary ? (
                    <span className="dim small">binary</span>
                  ) : (
                    <>
                      <ChangeCounts added={f.added} removed={f.removed} />
                      <DiffStat added={f.added} removed={f.removed} />
                    </>
                  )}
                </>
              );
              return (
                <li key={f.path}>
                  {noDiff ? (
                    <div className="file">{body}</div>
                  ) : (
                    <a className="file" href={href({ page: "sandbox", name: s.name, sheet: "diff", file: f.path })}>
                      {body}
                    </a>
                  )}
                </li>
              );
            })}
          </ul>
          {noDiff && <p className="note">Resume the sandbox to see the full diff. These are the changes from when it last ran.</p>}
        </>
      )}
      {detail.commits.length > 0 && (
        <>
          <div className="section-head">
            <span>Commits</span>
          </div>
          <ul className="panel commits">
            {detail.commits.map((c) => (
              <li key={c.hash}>
                <code className="hash">{c.hash}</code>
                <span className="commit-subject">{c.subject}</span>
                <span className="ago">{shortAgo(c.at)}</span>
              </li>
            ))}
          </ul>
        </>
      )}
      {(s.canPR || s.canCheckout) && (
        <div className="button-row">
          {s.canPR && (
            <button className="btn primary" onClick={() => act(s.name, (c) => c.openPR(s.name))}>
              <Icon name="pr" size={17} /> Open draft PR
            </button>
          )}
          {s.canCheckout && (
            <button className="btn" onClick={() => act(s.name, (c) => c.checkout(s.name))}>
              <Icon name="laptop" size={17} /> Check out on Mac
            </button>
          )}
        </div>
      )}
    </>
  );
}

function SetupTab({ s, detail }: { s: Sandbox; detail: Detail | null }) {
  const rows: [string, string][] = [["agent", s.agentLabel]];
  if (s.modelLabel) rows.push(["model", s.modelLabel]);
  rows.push(["workspace", s.clone ? "private clone" : "your working tree"]);
  if (s.branch) rows.push(["branch", s.branch]);
  rows.push(["kits", s.kits.length ? s.kits.join("\n") : "none"]);
  rows.push(["mcp", s.mcps.length ? s.mcps.join(", ") : "none"]);
  if (s.cpus && s.memoryMiB) rows.push(["resources", `${s.cpus} cpus, ${memoryLabel(s.memoryMiB)}`]);
  if (detail?.workspace) rows.push(["path", detail.workspace]);
  rows.push(["sandbox", s.name]);
  rows.push(["created", new Date(s.createdAt * 1000).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })]);
  return (
    <>
      <dl className="panel setup">
        {rows.map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
      </dl>
      <p className="note">Kits and MCP servers are fixed when a sandbox is created.</p>
      <Ports s={s} />
    </>
  );
}

function portLabel(p: Port) {
  const ip = p.hostIP ? (p.hostIP.includes(":") ? `[${p.hostIP}]:` : `${p.hostIP}:`) : "";
  return `${ip}${p.hostPort} → ${p.sandboxPort}${p.protocol ? `/${p.protocol}` : ""}`;
}

/** Ports published on the Mac with sbx ports, and a form to publish another. */
function Ports({ s }: { s: Sandbox }) {
  const { act } = useApp();
  const [sandboxPort, setSandboxPort] = useState("");
  const [hostPort, setHostPort] = useState("");
  const valid = (v: string) => /^\d+$/.test(v) && +v >= 1 && +v <= 65535;
  const ok = valid(sandboxPort) && (hostPort === "" || valid(hostPort));

  const publish = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ok || s.busy) return;
    const done = await act(s.name, (c) => c.publishPort(s.name, { sandboxPort: +sandboxPort, hostPort: hostPort ? +hostPort : undefined }));
    if (done) {
      setSandboxPort("");
      setHostPort("");
    }
  };

  return (
    <>
      <div className="section-head">
        <span>Ports</span>
      </div>
      {s.ports.length > 0 && (
        <ul className="panel ports">
          {s.ports.map((p) => (
            <li key={`${p.hostIP}-${p.hostPort}-${p.protocol}`}>
              <code>{portLabel(p)}</code>
              <button className="btn small" disabled={s.busy} onClick={() => act(s.name, (c) => c.unpublishPort(s.name, p))}>
                Unpublish
              </button>
            </li>
          ))}
        </ul>
      )}
      <form className="card port-form" onSubmit={publish}>
        <label className="field">
          <span>Sandbox port</span>
          <input className="mono" inputMode="numeric" placeholder="3000" value={sandboxPort} onChange={(e) => setSandboxPort(e.target.value.trim())} />
        </label>
        <label className="field">
          <span>Mac port</span>
          <input className="mono" inputMode="numeric" placeholder={sandboxPort || "Same"} value={hostPort} onChange={(e) => setHostPort(e.target.value.trim())} />
        </label>
        <div className="field">
          <button className="btn primary" type="submit" disabled={!ok || s.busy}>
            Publish
          </button>
        </div>
      </form>
      <p className="note">Forwards a port on the Mac to the sandbox, as <code>sbx ports --publish</code> does.</p>
    </>
  );
}

function relative(at: number): string {
  const s = Math.round(at - Date.now() / 1000);
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  const a = Math.abs(s);
  if (a < 60) return rtf.format(s, "second");
  if (a < 3600) return rtf.format(Math.round(s / 60), "minute");
  if (a < 86400) return rtf.format(Math.round(s / 3600), "hour");
  return rtf.format(Math.round(s / 86400), "day");
}

