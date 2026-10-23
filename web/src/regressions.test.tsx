// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { Sheet } from "./components";
import { AppProvider, useApp } from "./store";
import { Client, type Sandbox, type ServerEvent } from "./api";

vi.mock("./api", async (original) => ({
  ...await original<typeof import("./api")>(),
  savedToken: () => "test",
  serverInfo: () => new Promise(() => {}),
}));

let root: Root;
let host: HTMLDivElement;
let emit: (e: ServerEvent) => void;
let app: ReturnType<typeof useApp>;
function Probe() { app = useApp(); return null; }
const sandbox = (busy: boolean) => ({ name: "test", busy }) as Sandbox;
beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  vi.spyOn(Client.prototype, "events").mockImplementation((callback) => {
    emit = callback;
    return new Promise(() => {});
  });
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

it("keeps input focus on rerenders, uses the latest close callback, and restores focus", async () => {
  const trigger = document.createElement("button");
  document.body.append(trigger);
  trigger.focus();
  const close = vi.fn();
  let update: (value: string) => void;
  function Form() {
    const [value, setValue] = useState("");
    update = setValue;
    return <Sheet title="New" label="New" onClose={() => close(value)}><input value={value} onChange={e => setValue(e.target.value)} /></Sheet>;
  }
  await act(async () => root.render(<Form />));
  const input = host.querySelector("input")!;
  input.focus();
  await act(async () => update("typed"));
  expect(document.activeElement).toBe(input);
  window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  expect(close).toHaveBeenCalledWith("typed");
  await act(async () => root.render(null));
  expect(document.activeElement).toBe(trigger);
  trigger.remove();
});

for (const method of ["act", "create", "markSeen"] as const) {
  for (const event of ["sandbox", "removed", "snapshot", "unrelated", "none"] as const) {
    it(`${method} preserves ${event} streamed state before the response`, async () => {
      await act(async () => root.render(<AppProvider><Probe /></AppProvider>));
      await act(async () => emit({ type: "snapshot", sandboxes: [sandbox(true)] }));
      let resolve!: (s: Sandbox) => void;
      const response = new Promise<Sandbox>(r => { resolve = r; });
      vi.spyOn(app.client!, "create").mockReturnValue(response);
      vi.spyOn(app.client!, "seen").mockReturnValue(response);
      let request: Promise<unknown>;
      await act(async () => {
        request = method === "act" ? app.act("test", () => response)
          : method === "create" ? app.create({} as Parameters<typeof app.create>[0]) : app.markSeen("test");
      });
      await act(async () => {
        if (event === "sandbox") emit({ type: "sandbox", sandbox: sandbox(false) });
        if (event === "removed") emit({ type: "removed", name: "test" });
        if (event === "snapshot") emit({ type: "snapshot", sandboxes: [] });
        if (event === "unrelated") emit({ type: "sandbox", sandbox: { ...sandbox(false), name: "other" } });
        resolve(sandbox(true));
        await request;
      });
      if (event === "removed" || event === "snapshot") expect(app.sandboxes.test).toBeUndefined();
      else expect(app.sandboxes.test.busy).toBe(event !== "sandbox");
    });
  }
}
