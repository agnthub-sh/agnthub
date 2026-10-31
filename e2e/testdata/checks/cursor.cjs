// Runs cursor-agent's own workspace subagent loader, sliced out of its shipped
// bundle, against a workspace and prints what it loads as JSON.
//
// cursor-agent has no offline command that lists agents (every command needs a
// signed-in account), so this evaluates the bundle's loader class (the one with
// computeAgentsDirs, which reads the workspace's agent folders and parses each
// file with Cursor's line-based frontmatter reader) with Node's path/fs standing
// in for the bundle's module imports. If a new cursor-agent build changes the
// loader beyond recognition this fails loudly instead of passing.
//
// usage: node cursor.cjs <cursor-agent install dir> <workspace>
"use strict";
const fs = require("node:fs");
const path = require("node:path");
const util = require("node:util");

const [installDir, workspace] = process.argv.slice(2);
const bundle = fs.readFileSync(path.join(installDir, "index.js"), "utf8");

const marker = bundle.indexOf("computeAgentsDirs(){");
if (marker < 0) throw new Error("cursor-agent bundle has no computeAgentsDirs loader");
const classStart = bundle.lastIndexOf("class ", marker);
const className = /^class ([A-Za-z_$][\w$]*)/.exec(bundle.slice(classStart))[1];
const anchor = bundle.lastIndexOf('("local-subagents")', classStart);
if (anchor < 0) throw new Error("cursor-agent bundle has no local-subagents loader");
const regionStart = bundle.lastIndexOf("const ", anchor);
const regionEnd = matchBrace(bundle, bundle.indexOf("{", classStart));
const region = bundle.slice(regionStart, regionEnd + 1);

// matchBrace returns the index of the brace closing the one at open, skipping
// string literals (the loader has no template or regex literals with braces).
function matchBrace(text, open) {
  let depth = 0;
  for (let i = open; i < text.length; i++) {
    const c = text[i];
    if (c === '"' || c === "'" || c === "`") {
      for (i++; i < text.length && text[i] !== c; i++) if (text[i] === "\\") i++;
      continue;
    }
    if (c === "{") depth++;
    if (c === "}" && --depth === 0) return i;
  }
  throw new Error("unbalanced loader class in cursor-agent bundle");
}

// Stand-ins for the bundle's (minified) module imports, by what the loader does
// with them: path helpers and stat by name, a file lister called with
// {rootDir, includeFile}, a text reader called with a path, value classes built
// with new, and enum members read as strings.
function listFiles(dir, include) {
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return [];
  }
  return entries.flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) return listFiles(full, include);
    return entry.isFile() && include(full) ? [full] : [];
  });
}
const named = {
  join: path.join,
  resolve: path.resolve,
  basename: path.basename,
  extname: path.extname,
  stat: fs.promises.stat,
  debuglog: util.debuglog,
  READONLY: "readonly",
  DEFAULT: "default",
};
function stub() {
  return new Proxy(function () {}, {
    get(_, key) {
      if (typeof key === "symbol") return undefined;
      return key in named ? named[key] : stub();
    },
    apply(_, __, args) {
      const [first] = args;
      if (first && typeof first === "object" && "rootDir" in first) {
        return Promise.resolve(listFiles(first.rootDir, first.includeFile));
      }
      if (typeof first === "string") return fs.promises.readFile(first, "utf8");
      return stub();
    },
    construct(_, args) {
      return { ...args[0] };
    },
  });
}
const scope = new Proxy(
  {},
  {
    has: (_, key) => key !== "__scope",
    get: (_, key) => {
      if (typeof key === "symbol") return undefined; // no Symbol.unscopables
      return key in globalThis ? globalThis[key] : stub();
    },
  },
);
// eslint-disable-next-line no-new-func
const Loader = new Function("__scope", `with (__scope) { ${region}; return ${className}; }`)(scope);

(async () => {
  const loader = new Loader(workspace, () => true);
  const agents = await loader.getAllSubagents();
  process.stdout.write(
    JSON.stringify({
      dirs: loader.computeAgentsDirs().map((d) => d.dirPath),
      agents: agents.map((a) => ({
        name: a.name,
        description: a.description,
        fullPath: a.fullPath,
        tools: a.tools,
        permissionMode: a.permissionMode,
        source: a.source,
      })),
    }) + "\n",
  );
})().catch((err) => {
  process.stderr.write(String(err && err.stack ? err.stack : err) + "\n");
  process.exit(1);
});
