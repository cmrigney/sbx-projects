import { Suspense, lazy, useSyncExternalStore } from "react";
import { useApp } from "./store";
import { Empty, Toasts, useRoute } from "./components";
import { NewProject } from "./views/NewProject";
import { NewSandbox } from "./views/NewSandbox";
import { Pairing } from "./views/Pairing";
import { SandboxDetail } from "./views/SandboxDetail";
import { SandboxList } from "./views/SandboxList";
import { DiffSheet, RemoveSheet, ReplySheet } from "./views/Sheets";

// xterm.js is most of the app's size, so it loads on the first attach.
const TerminalView = lazy(() => import("./views/Terminal").then((m) => ({ default: m.TerminalView })));

// Past this width the list and the open sandbox sit side by side.
const wideQuery = "(min-width: 900px)";

function useWide() {
  return useSyncExternalStore(
    (fn) => {
      const m = matchMedia(wideQuery);
      m.addEventListener("change", fn);
      return () => m.removeEventListener("change", fn);
    },
    () => matchMedia(wideQuery).matches,
  );
}

export function App() {
  const { client, sandboxes, server } = useApp();
  const route = useRoute();
  const wide = useWide();

  if (!client) return <Pairing />;

  const name = route.page === "sandbox" ? route.name : undefined;
  const open = name ? sandboxes[name] : undefined;
  const showList = wide || !name;

  return (
    <div className={`shell ${wide ? "wide" : "narrow"} ${name ? "has-detail" : ""}`}>
      {showList && (
        <aside className="pane list-pane">
          <SandboxList selected={name} />
        </aside>
      )}
      {name ? (
        <main className="pane detail-pane" key={`${server?.id}-${name}`}>
          <SandboxDetail name={name} wide={wide} />
        </main>
      ) : (
        wide && (
          <main className="pane detail-pane">
            <Empty icon="terminal" title="No sandbox selected">
              Pick a sandbox to see what its agent is doing.
            </Empty>
          </main>
        )
      )}
      {route.page === "new" && <NewSandbox project={route.project} />}
      {route.page === "new-project" && <NewProject />}
      {route.page === "sandbox" && open && route.sheet === "reply" && <ReplySheet sandbox={open} />}
      {route.page === "sandbox" && open && route.sheet === "diff" && <DiffSheet sandbox={open} file={route.file} />}
      {route.page === "sandbox" && open && route.sheet === "remove" && <RemoveSheet sandbox={open} />}
      {route.page === "sandbox" && open && (route.sheet === "terminal" || route.sheet === "shell") && (
        <Suspense fallback={<div className="term-page" />}>
          <TerminalView key={route.sheet} sandbox={open} shell={route.sheet === "shell"} />
        </Suspense>
      )}
      <Toasts />
    </div>
  );
}
