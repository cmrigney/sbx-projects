import { useEffect, useRef, useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Diff, Reply, Sandbox } from "../api";
import { useApp } from "../store";
import { Composer, Empty, Icon, Sheet, back, go, shortAgo } from "../components";

/** The agent's last reply in full, as the TUI's t shows it. Opening it clears the unread mark. */
export function ReplySheet({ sandbox: s }: { sandbox: Sandbox }) {
  const { client, markSeen } = useApp();
  const [reply, setReply] = useState<Reply | null>(null);
  const [error, setError] = useState<string>();
  const [copied, setCopied] = useState(false);
  const close = () => back({ page: "sandbox", name: s.name });

  useEffect(() => {
    client?.reply(s.name).then(
      (r) => {
        setReply(r);
        markSeen(s.name);
      },
      (e) => setError(e.message),
    );
  }, [client, s.name, markSeen]);

  const copy = async () => {
    if (!reply) return;
    await navigator.clipboard?.writeText(reply.markdown);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <Sheet
      label="Last reply"
      onClose={close}
      title={<><Icon name="bubble" size={16} /> Last reply</>}
      subtitle={reply && `${s.agentLabel} · ${shortAgo(reply.at) === "now" ? "just now" : `${shortAgo(reply.at)} ago`}`}
      leading={
        <button className="ghost" onClick={copy} disabled={!reply || !navigator.clipboard}>
          <Icon name="copy" size={16} />
          {copied ? "Copied" : "Copy"}
        </button>
      }
      trailing={<button className="ghost icon-btn" aria-label="Close" onClick={close}><Icon name="close" size={18} /></button>}
      footer={
        <div className="composer-dock in-sheet">
          <Composer sandbox={s} placeholder={`Reply to ${s.agentLabel}`} onSent={close} />
        </div>
      }
    >
      {reply ? (
        <div className="markdown">
          {/* react-markdown builds elements rather than HTML, so a reply can't inject markup. */}
          <Markdown remarkPlugins={[remarkGfm]} components={{ a: (p) => <a {...p} target="_blank" rel="noreferrer" />, table: (p) => <div className="table-wrap"><table {...p} /></div> }}>
            {reply.markdown}
          </Markdown>
        </div>
      ) : error ? (
        <Empty icon="bubble" title="No reply">{error}</Empty>
      ) : (
        <div className="loading"><span className="spinner" /></div>
      )}
    </Sheet>
  );
}

/** The full diff behind the Changes tab, scrolled to one file. */
export function DiffSheet({ sandbox: s, file }: { sandbox: Sandbox; file?: string }) {
  const { client } = useApp();
  const [diff, setDiff] = useState<Diff | null>(null);
  const [error, setError] = useState<string>();
  const body = useRef<HTMLDivElement>(null);

  useEffect(() => {
    client?.diff(s.name).then(setDiff, (e) => setError(e.message));
  }, [client, s.name]);

  useEffect(() => {
    if (diff && file) document.getElementById(`diff-${file}`)?.scrollIntoView({ block: "start" });
  }, [diff, file]);

  return (
    <Sheet
      label="Changes"
      onClose={() => back({ page: "sandbox", name: s.name })}
      title={<><Icon name="branch" size={16} /> Changes</>}
      subtitle={diff && `${diff.files.length} ${diff.files.length === 1 ? "file" : "files"}`}
      trailing={<button className="ghost icon-btn" aria-label="Close" onClick={() => back({ page: "sandbox", name: s.name })}><Icon name="close" size={18} /></button>}
    >
      <div ref={body} className="diff">
        {diff ? (
          <>
            {diff.truncated && <p className="note pad">This diff is too long to show in full. Files past the limit show only their names.</p>}
            {diff.files.length === 0 && <p className="note pad center">No changes.</p>}
            {diff.files.map((f) => (
              <section key={f.path} id={`diff-${f.path}`}>
                <h4 className="diff-file">
                  <code>{f.path}</code>
                  {f.note && <span className="muted small">{f.note}</span>}
                </h4>
                {f.lines.length === 0 ? (
                  <p className="note pad">{f.note ? `Nothing to show for a ${f.note} file` : "No lines to show"}</p>
                ) : (
                  // Long lines scroll sideways rather than wrap, so the code's columns stay lined up.
                  <pre className="hunks">
                    {f.lines.map((l, i) => (
                      <span key={i} className={l.startsWith("@@") ? "hunk" : l.startsWith("+") ? "ins" : l.startsWith("-") ? "rm" : ""}>
                        {l || " "}
                        {"\n"}
                      </span>
                    ))}
                  </pre>
                )}
              </section>
            ))}
          </>
        ) : error ? (
          <Empty icon="warn" title="Couldn't read the diff">{error}</Empty>
        ) : (
          <div className="loading"><span className="spinner" /><p className="muted">Reading the diff</p></div>
        )}
      </div>
    </Sheet>
  );
}

export function RemoveSheet({ sandbox: s }: { sandbox: Sandbox }) {
  const { act } = useApp();
  let message = s.clone
    ? "This deletes the sandbox, its private clone and agent history. Commits that were not pushed are lost."
    : "This deletes the sandbox and its agent history.";
  if (s.running) message += " The sandbox is stopped first.";
  return (
    <div className="backdrop" onClick={() => back({ page: "sandbox", name: s.name })}>
      <div className="dialog" role="alertdialog" aria-label={`Remove ${s.name}?`} onClick={(e) => e.stopPropagation()}>
        <h3>Remove {s.name}?</h3>
        <p>{message}</p>
        <button
          className="btn danger"
          autoFocus
          onClick={async () => {
            if (await act(s.name, (c) => c.remove(s.name))) go({ page: "list" }, true);
          }}
        >
          Remove
        </button>
        <button className="btn" onClick={() => back({ page: "sandbox", name: s.name })}>
          Cancel
        </button>
      </div>
    </div>
  );
}
