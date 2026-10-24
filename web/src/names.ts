// The names attached files get in the sandbox. These are the API's
// safeName and uniqueName (internal/api/upload.go), so the name a message
// mentions is the one the Mac copies the file to. The cases in
// internal/api/testdata/names.json keep the copies in step.

/** The longest name, in code points, a file is given. */
const maxName = 120;

const kept = /^[\p{L}\p{Nd}._-]$/u;

/** The last "." and what follows it, as Go's path.Ext gives it. */
function ext(name: string) {
  const dot = name.lastIndexOf(".");
  return dot < 0 ? "" : name.slice(dot);
}

/**
 * Keeps the last element of the name and replaces anything a shell or the
 * agent's prompt could trip on. A name it returns comes back unchanged.
 */
export function safeName(name: string): string {
  name = name.slice(Math.max(name.lastIndexOf("/"), name.lastIndexOf("\\")) + 1);
  name = Array.from(name, (c) => (kept.test(c) ? c : "_")).join("").replace(/^\.+/, "");
  const chars = Array.from(name);
  if (chars.length > maxName) {
    let e = Array.from(ext(name));
    if (e.length > 20) e = [];
    name = chars.slice(0, maxName - e.length).join("") + e.join("");
  }
  return /^_*$/.test(name) ? "file" : name;
}

/** Numbers a name already taken, shortening it to keep within maxName. */
export function uniqueName(name: string, taken: Set<string>): string {
  const e = ext(name);
  const base = Array.from(name.slice(0, name.length - e.length));
  let out = name;
  for (let i = 2; taken.has(out); i++) {
    const suffix = `-${i}${e}`;
    const keep = Math.max(maxName - Array.from(suffix).length, 0);
    out = base.slice(0, keep).join("") + suffix;
  }
  taken.add(out);
  return out;
}
