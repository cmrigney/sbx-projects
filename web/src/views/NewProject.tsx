import { useEffect, useState } from "react";
import type { ProjectOptions } from "../api";
import { useApp } from "../store";
import { Icon, Sheet, back, go } from "../components";

// As internal/core checks it: one plain folder name.
const validName = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

/** The folder a clone of url goes in, as the Mac names it. */
function repoName(url: string): string {
  const u = url.trim().replace(/\/+$/, "").replace(/\.git$/, "");
  return u.slice(Math.max(u.lastIndexOf("/"), u.lastIndexOf(":")) + 1);
}

/**
 * Makes a project on the Mac: an empty repo, or a clone. The Mac picks the
 * folder, so the form only asks for a name. With a first instruction, an
 * agent starts on it straight away.
 */
export function NewProject() {
  const { client, server, newProject } = useApp();
  const [options, setOptions] = useState<ProjectOptions | null>(null);
  const [from, setFrom] = useState<"empty" | "clone">("empty");
  const [url, setUrl] = useState("");
  const [name, setName] = useState("");
  const [publish, setPublish] = useState(false);
  const [task, setTask] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    if (!client) return;
    let stale = false;
    client.projectOptions().then(
      (o) => !stale && setOptions(o),
      () => {},
    );
    return () => {
      stale = true;
    };
  }, [client]);

  const clone = from === "clone";
  const folder = name.trim() || (clone ? repoName(url) : "");
  const nameError = folder && !validName.test(folder) ? "Use letters, digits, '.', '-' and '_', starting with a letter or digit." : undefined;
  const canCreate = !creating && folder !== "" && !nameError && (!clone || url.trim() !== "");
  const close = () => back({ page: "list" });

  const submit = async () => {
    if (!canCreate) return;
    setCreating(true);
    setError(undefined);
    try {
      const res = await newProject({
        name: name.trim(),
        cloneUrl: clone ? url.trim() : "",
        publish: !clone && publish,
        task: task.trim(),
      });
      if (res.sandbox) go({ page: "sandbox", name: res.sandbox.name }, true);
      else go({ page: "new", project: res.project.id }, true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setCreating(false);
    }
  };

  return (
    <Sheet
      label="New project"
      onClose={() => !creating && close()}
      title={<><Icon name="box" size={16} /> New project</>}
      leading={<button className="ghost" onClick={close} disabled={creating}>Cancel</button>}
      trailing={
        creating ? (
          <span className="spinner small" />
        ) : (
          <button className="btn primary small" onClick={submit} disabled={!canCreate}>
            Create
          </button>
        )
      }
    >
      <form
        className="form"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <fieldset>
          <legend>Start from</legend>
          <div className="card">
            <div className="field">
              <div className="segmented small">
                <button type="button" className={!clone ? "on" : ""} onClick={() => setFrom("empty")}>Empty repo</button>
                <button type="button" className={clone ? "on" : ""} onClick={() => setFrom("clone")}>Clone a repo</button>
              </div>
            </div>
            {clone && (
              <label className="field">
                <input
                  className="mono"
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  placeholder="https://github.com/owner/repo"
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck={false}
                  inputMode="url"
                  aria-label="Repository URL"
                />
              </label>
            )}
            <label className="field">
              <span>Name</span>
              <input
                className="mono"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={clone ? repoName(url) || "From the URL" : "my-app"}
                autoCapitalize="off"
                autoCorrect="off"
                spellCheck={false}
                aria-label="Project name"
              />
            </label>
            {!clone && options?.canPublish && (
              <label className="field">
                <span>Publish to GitHub as a private repo</span>
                <input type="checkbox" role="switch" className="switch" checked={publish} onChange={(e) => setPublish(e.target.checked)} />
              </label>
            )}
          </div>
          <p className={`footnote ${nameError ? "red" : ""}`}>
            {nameError ??
              (options
                ? `Made in ${options.dir}/${folder || "…"} on ${server?.name ?? "the Mac"}.`
                : `Made in your projects folder on ${server?.name ?? "the Mac"}.`)}
            {!clone && !nameError && " It starts with a README on main, so agents have a branch to start from."}
            {!clone && options && !options.canPublish && " To publish to GitHub from here, install gh on the Mac and sign in."}
          </p>
        </fieldset>

        <div>
          <div className="card">
            <textarea className="task" rows={3} placeholder="What should the agent build? (optional)" value={task} onChange={(e) => setTask(e.target.value)} />
          </div>
          <p className="footnote">
            Starts Claude Code on a private clone. Leave it empty to choose the agent and its setup next.
          </p>
        </div>

        {error && <div className="card"><p className="field red">{error}</p></div>}
        <button type="submit" hidden />
      </form>
    </Sheet>
  );
}
