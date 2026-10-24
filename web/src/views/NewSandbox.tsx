import { useEffect, useState } from "react";
import type { Options } from "../api";
import { useApp } from "../store";
import { Icon, Sheet, back, go } from "../components";

/**
 * The TUI's New sandbox form. It starts with what the project remembers:
 * its kits, and the model and MCP servers last picked.
 */
export function NewSandbox({ project }: { project?: number }) {
  const { projects, client, create } = useApp();
  const [projectId, setProjectId] = useState<number | undefined>(project ?? projects[0]?.id);
  const [options, setOptions] = useState<Options | null>(null);
  const [optionsError, setOptionsError] = useState<string>();
  const [task, setTask] = useState("");
  const [source, setSource] = useState<"branch" | "pr">("branch");
  const [value, setValue] = useState("");
  const [clone, setClone] = useState(true);
  const [agent, setAgent] = useState("claude");
  const [models, setModels] = useState<Record<string, string>>({});
  const [kits, setKits] = useState<string[]>([]);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [newKit, setNewKit] = useState("");
  const [mcps, setMcps] = useState<Set<string>>(new Set());
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    if (projectId === undefined && projects[0]) setProjectId(projects[0].id);
  }, [projects, projectId]);

  useEffect(() => {
    if (!client || projectId === undefined) return;
    let stale = false;
    setOptions(null);
    setOptionsError(undefined);
    client.options(projectId).then(
      (o) => {
        if (stale) return;
        setOptions(o);
        setAgent(o.defaults.agent);
        setModels(o.defaults.models);
        setClone(o.defaults.clone);
        setKits(o.defaults.kits);
        setPicked(new Set(o.defaults.kits));
        setMcps(new Set(o.defaults.mcps));
      },
      (e) => !stale && setOptionsError(e.message),
    );
    return () => {
      stale = true;
    };
  }, [client, projectId]);

  const agentOption = options?.agents.find((a) => a.id === agent);
  // As in the TUI, only a pull request needs the field filled in.
  const canCreate = !creating && options !== null && (source !== "pr" || value.trim() !== "");
  const close = () => back({ page: "list" });

  const addKit = () => {
    const k = newKit.trim();
    if (!k) return;
    if (!kits.includes(k)) setKits([...kits, k]);
    setPicked(new Set(picked).add(k));
    setNewKit("");
  };

  const toggle = (set: Set<string>, item: string, on: boolean) => {
    const next = new Set(set);
    if (on) next.add(item);
    else next.delete(item);
    return next;
  };

  const submit = async () => {
    if (!canCreate || projectId === undefined || !options) return;
    setCreating(true);
    setError(undefined);
    try {
      const s = await create({
        projectId,
        agent,
        source,
        value: value.trim(),
        task: task.trim(),
        clone: source === "pr" || clone,
        kits: kits.filter((k) => picked.has(k)),
        model: agentOption?.models.length ? (models[agent] ?? "") : "",
        mcps: (options.mcpServers ?? []).map((m) => m.name).filter((n) => mcps.has(n)),
      });
      go({ page: "sandbox", name: s.name }, true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setCreating(false);
    }
  };

  return (
    <Sheet
      label="New sandbox"
      onClose={() => !creating && close()}
      title={<><Icon name="box" size={16} /> New sandbox</>}
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
        {projects.length > 1 && (
          <div className="card">
            <label className="field">
              <span>Project</span>
              <select value={projectId} onChange={(e) => setProjectId(Number(e.target.value))}>
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>{p.name}</option>
                ))}
              </select>
            </label>
          </div>
        )}

        <div>
          <div className="card">
            <textarea className="task" rows={3} placeholder="What should the agent do?" value={task} onChange={(e) => setTask(e.target.value)} />
          </div>
          <p className="footnote">The agent's first instruction, and the sandbox's title.</p>
        </div>

        <fieldset>
          <legend>Start from</legend>
          <div className="card">
            <div className="field">
              <div className="segmented small">
                <button type="button" className={source === "branch" ? "on" : ""} onClick={() => setSource("branch")}>New branch</button>
                <button type="button" className={source === "pr" ? "on" : ""} onClick={() => setSource("pr")}>Pull request</button>
              </div>
            </div>
            <label className="field">
              <input
                className="mono"
                value={value}
                onChange={(e) => setValue(e.target.value)}
                placeholder={source === "branch" ? "Branch (optional), like feat/policy-log-json" : "PR number or URL"}
                autoCapitalize="off"
                autoCorrect="off"
                spellCheck={false}
                inputMode={source === "pr" ? "url" : "text"}
                aria-label={source === "branch" ? "Branch" : "Pull request"}
              />
            </label>
            {source === "branch" && (
              <label className="field">
                <span>Private clone</span>
                <Switch checked={clone} onChange={setClone} />
              </label>
            )}
          </div>
        </fieldset>

        {options ? (
          <>
            <fieldset>
              <legend>Agent</legend>
              <div className="card">
                <label className="field">
                  <span>Agent</span>
                  <select value={agent} onChange={(e) => setAgent(e.target.value)}>
                    {options.agents.map((a) => (
                      <option key={a.id} value={a.id}>{a.label}</option>
                    ))}
                  </select>
                </label>
                {agentOption && agentOption.models.length > 0 && (
                  <label className="field">
                    <span>Model</span>
                    <select value={models[agent] ?? ""} onChange={(e) => setModels({ ...models, [agent]: e.target.value })}>
                      {agentOption.models.map((m) => (
                        <option key={m.id} value={m.id}>{m.label}</option>
                      ))}
                    </select>
                  </label>
                )}
              </div>
            </fieldset>

            <fieldset>
              <legend>Kits</legend>
              <div className="card">
                {kits.map((k) => (
                  <label key={k} className="field">
                    <code className="truncate">{k}</code>
                    <Switch checked={picked.has(k)} onChange={(on) => setPicked(toggle(picked, k, on))} />
                  </label>
                ))}
                <div className="field">
                  <input
                    className="mono"
                    value={newKit}
                    onChange={(e) => setNewKit(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") {
                        e.preventDefault();
                        addKit();
                      }
                    }}
                    placeholder="Add a kit reference"
                    autoCapitalize="off"
                    autoCorrect="off"
                    spellCheck={false}
                    aria-label="Kit reference"
                  />
                  <button type="button" className="ghost" onClick={addKit} disabled={!newKit.trim()}>Add</button>
                </div>
              </div>
              <p className="footnote">The project's defaults come first. Each kit is a directory, ZIP, git URL or OCI image on the Mac.</p>
            </fieldset>

            <fieldset>
              <legend>MCP servers</legend>
              <div className="card">
                {options.mcpServers === null ? (
                  <p className="field muted">{options.mcpError ?? "Couldn't list the MCP servers."}</p>
                ) : options.mcpServers.length === 0 ? (
                  <p className="field muted">No MCP servers are registered. Add them with sbx mcp add on your Mac.</p>
                ) : (
                  options.mcpServers.map((m) => (
                    <label key={m.name} className="field">
                      <span className="stack">
                        <span>{m.name}</span>
                        {!m.ready && (
                          <small className="orange">{m.nextStep ? `Needs setup. Run ${m.nextStep} on your Mac` : (m.detail ?? m.status)}</small>
                        )}
                      </span>
                      <Switch checked={mcps.has(m.name)} disabled={!m.ready && !mcps.has(m.name)} onChange={(on) => setMcps(toggle(mcps, m.name, on))} />
                    </label>
                  ))
                )}
              </div>
              <p className="footnote">Fixed when the sandbox is created.</p>
            </fieldset>
          </>
        ) : (
          <div className="card">
            <p className="field muted">{optionsError ?? <><span className="spinner small" /> Loading the Mac's options</>}</p>
          </div>
        )}

        {error && <div className="card"><p className="field red">{error}</p></div>}
        <button type="submit" hidden />
      </form>
    </Sheet>
  );
}

function Switch({ checked, onChange, disabled }: { checked: boolean; onChange: (on: boolean) => void; disabled?: boolean }) {
  return <input type="checkbox" role="switch" className="switch" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />;
}
