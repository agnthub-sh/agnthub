// Loads the project and user agent directories with Gemini CLI's own agent
// loader and storage paths, imported from its installed bundle, and prints what
// loads (and every loading error) as JSON. Gemini CLI has no offline command that
// lists agents; this is the code its agent registry runs.
//
// usage: node gemini.mjs   (in the workspace, with HOME / GEMINI_CLI_HOME set)
import { execFileSync } from "node:child_process";
import { readdirSync } from "node:fs";
import { join } from "node:path";

const root = execFileSync("npm", ["root", "-g"]).toString().trim();
const bundle = join(root, "@google/gemini-cli", "bundle");
let core;
for (const name of readdirSync(bundle).filter((f) => /^core-.*\.js$/.test(f)).sort()) {
  const mod = await import(join(bundle, name));
  if (typeof mod.loadAgentsFromDirectory === "function" && mod.Storage) {
    core = mod;
    break;
  }
}
if (!core) throw new Error("no Gemini CLI core module exports loadAgentsFromDirectory");

const dirs = {
  project: new core.Storage(process.cwd()).getProjectAgentsDir(),
  user: core.Storage.getUserAgentsDir(),
};
const out = {};
for (const [scope, dir] of Object.entries(dirs)) {
  const loaded = await core.loadAgentsFromDirectory(dir);
  out[scope] = {
    dir,
    agents: loaded.agents.map((a) => ({ name: a.name, kind: a.kind, tools: a.toolConfig?.tools })),
    errors: loaded.errors.map((e) => e.message),
  };
}
process.stdout.write(JSON.stringify(out) + "\n");
