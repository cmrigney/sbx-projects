import { useRef, useState } from "react";
import { groupLabel, groups, type Sandbox, type StatusGroup } from "../api";
import { sandboxesIn, shows, useApp } from "../store";
import { ChangeCounts, Empty, Icon, StatusMark, go, href, shortAgo, stateColor, useKeys, useTick } from "../components";

export function SandboxList({ selected }: { selected?: string }) {
  const { projects, sandboxes, connection } = useApp();
  // Null shows every sandbox but the parked ones, which have their own filter.
  const [filter, setFilter] = useState<StatusGroup | null>(null);
  const [query, setQuery] = useState("");
  const search = useRef<HTMLInputElement>(null);
  useTick();

  const sections = projects.map((p) => ({ project: p, rows: sandboxesIn(sandboxes, p, filter, query) })).filter((s) => s.rows.length > 0);
  const live = connection.kind === "live";

  useKeys({
    n: () => go({ page: projects.length > 0 ? "new" : "new-project" }),
    N: () => go({ page: "new-project" }),
    "/": () => search.current?.focus(),
  });

  return (
    <div className="page list-page">
      <header className="bar brand-bar">
        <a className="brand" href={href({ page: "list" })} aria-label="sbx-projects">
          <img src="/icon.svg" alt="" width={30} height={30} />
          <span>
            sbx<b>-projects</b>
          </span>
        </a>
        <HostChip />
      </header>
      <div className="list-head">
        <h1>
          Sandboxes <span className="count">{Object.keys(sandboxes).length}</span>
        </h1>
        <div className="list-actions">
          <button className="ghost new-project" onClick={() => go({ page: "new-project" })} title="New project (N)" aria-label="New project">
            <Icon name="folderPlus" size={20} /> <span className="label">Project</span>
          </button>
          <button className="btn primary small" disabled={projects.length === 0} onClick={() => go({ page: "new" })}>
            <Icon name="plus" size={16} /> New <kbd className="hint">n</kbd>
          </button>
        </div>
      </div>
      <label className="search">
        <Icon name="search" size={16} />
        <input
          ref={search}
          type="search"
          placeholder="Filter by title, branch or name"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => e.key === "Escape" && (setQuery(""), e.currentTarget.blur())}
        />
        <kbd className="hint">/</kbd>
      </label>
      <div className="filters" role="toolbar" aria-label="Filter">
        <Filter label="Active" group={null} filter={filter} setFilter={setFilter} />
        {groups.map((g) => (
          <Filter key={g} label={groupLabel[g]} group={g} filter={filter} setFilter={setFilter} />
        ))}
      </div>
      <ConnectionBanner />
      {sections.map(({ project, rows }) => (
        <section key={project.id} className="project">
          <h2 className="project-head">
            <Icon name="box" size={16} />
            <span className="project-name">{project.name}</span>
            {project.remote && <span className="project-remote">{project.remote.replace("github.com/", "")}</span>}
          </h2>
          <ul className="cards">
            {rows.map((s) => (
              <li key={s.name}>
                <SandboxCard sandbox={s} selected={s.name === selected} />
              </li>
            ))}
          </ul>
        </section>
      ))}
      {live && projects.length === 0 && (
        <Empty icon="terminal" title="No projects yet">
          Start one from scratch or clone a repo onto your Mac.
          <button className="btn primary small empty-action" onClick={() => go({ page: "new-project" })}>
            <Icon name="plus" size={16} /> New project
          </button>
        </Empty>
      )}
      {live && projects.length > 0 && Object.keys(sandboxes).length === 0 && (
        <Empty icon="prompt" title="No sandboxes">
          Start an agent on a new branch with New.
        </Empty>
      )}
      {live && Object.keys(sandboxes).length > 0 && sections.length === 0 && filter === null && !query.trim() && (
        <Empty icon="prompt" title="Nothing active">
          Every sandbox is parked. Choose Parked to see them.
        </Empty>
      )}
      {live && Object.keys(sandboxes).length > 0 && sections.length === 0 && (filter !== null || query.trim()) && (
        <Empty icon="search" title="Nothing matches">
          Try another filter or search.
        </Empty>
      )}
      {connection.kind === "connecting" && projects.length === 0 && <div className="loading"><span className="spinner" /></div>}
    </div>
  );
}

function Filter({
  label,
  group,
  filter,
  setFilter,
}: {
  label: string;
  group: StatusGroup | null;
  filter: StatusGroup | null;
  setFilter: (g: StatusGroup | null) => void;
}) {
  const { sandboxes } = useApp();
  const n = Object.values(sandboxes).filter((s) => shows(group, s)).length;
  const on = filter === group;
  return (
    <button className={`filter ${on ? "on" : ""} ${group ?? "all"}`} aria-pressed={on} onClick={() => setFilter(group)}>
      {label}
      <span className="n">{n}</span>
    </button>
  );
}

/** Which Mac this is, and whether it's connected. */
function HostChip() {
  const { server, connection } = useApp();
  const word = { live: "live", connecting: "connecting", offline: "offline", signedOut: "signed out" }[connection.kind];
  return (
    <div className={`host ${connection.kind}`} title={`${server?.name ?? "Mac"}: ${word}`}>
      <span className="dot" />
      <span className="host-name">{server?.name ?? "Mac"}</span>
    </div>
  );
}

/** Says why the list may be stale, and what to do about it. */
export function ConnectionBanner() {
  const { connection, server, listError, signOut } = useApp();
  const mac = server?.name ?? "the Mac";
  if (connection.kind === "offline") {
    return (
      <div className="banner warn">
        <Icon name="wifi" size={20} />
        <div>
          <strong>Can't reach {mac}</strong>
          <p>Check that sbx-projects api is running and you're on the same network. {connection.message}</p>
        </div>
      </div>
    );
  }
  if (connection.kind === "signedOut") {
    return (
      <div className="banner bad">
        <Icon name="lock" size={20} />
        <div>
          <strong>Signed out</strong>
          <p>{connection.message}</p>
          <button className="link" onClick={signOut}>Pair again</button>
        </div>
      </div>
    );
  }
  if (listError) {
    return (
      <div className="banner warn">
        <Icon name="warn" size={20} />
        <div>
          <strong>Can't list sandboxes on {mac}</strong>
          <p>The states below may be out of date. {listError}</p>
        </div>
      </div>
    );
  }
  return null;
}

function SandboxCard({ sandbox: s, selected }: { sandbox: Sandbox; selected: boolean }) {
  const loud = s.state === "needsYou" || s.state === "failed";
  return (
    <a
      className={`card sandbox ${selected ? "selected" : ""} ${s.unread ? "unread" : ""}`}
      style={{ "--rail": stateColor[s.state] } as React.CSSProperties}
      href={href({ page: "sandbox", name: s.name })}
      aria-current={selected ? "page" : undefined}
    >
      <StatusMark state={s.state} />
      <span className="sandbox-text">
        <span className="sandbox-top">
          <span className="sandbox-title">{s.title}</span>
          {s.activeAt ? <span className="ago">{shortAgo(s.activeAt)}</span> : null}
        </span>
        <span className={`sandbox-status ${loud ? "loud" : ""}`}>
          {s.unread && <span className="new-tag">new reply</span>}
          {s.statusText}
        </span>
        <span className="sandbox-meta">
          {s.branch && (
            <span className="branch">
              <Icon name="branch" size={13} />
              {s.branch}
            </span>
          )}
          {s.files > 0 && <ChangeCounts added={s.added} removed={s.removed} />}
        </span>
      </span>
    </a>
  );
}
