// Runs omp's own agent discovery (src/task/discovery.ts of the installed
// package) for the current directory and prints the agents it finds as JSON.
// omp has no offline command that lists agents; this is the function its task
// tool uses.
//
// usage: bun omp.ts <omp package dir>   (in the workspace, with HOME set)
const pkg = process.argv[2];
const { discoverAgents } = await import(`${pkg}/src/task/discovery.ts`);
const found = await discoverAgents(process.cwd());
process.stdout.write(
  JSON.stringify({
    agents: found.agents.map((a: { name: string; source: string; filePath: string; tools?: string[] }) => ({
      name: a.name,
      source: a.source,
      filePath: a.filePath,
      tools: a.tools,
    })),
  }) + "\n",
);
