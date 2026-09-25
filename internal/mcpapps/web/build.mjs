import { build, transform } from "esbuild";
import { readFile, writeFile, mkdir, readdir } from "node:fs/promises";
import { createHash } from "node:crypto";
import { gzipSync } from "node:zlib";
import { fileURLToPath } from "node:url";
import path from "node:path";

// 单入口单视图，完全内联；Go embed 的产物由锁定依赖生成，无运行时 Node/CDN。
const root = path.dirname(fileURLToPath(import.meta.url));
const output = path.resolve(root, "../assets");
const notices = (
  await readFile(path.join(root, "THIRD_PARTY_NOTICES.txt"), "utf8")
).replaceAll("--", "- -");
const views = {
  agentdock_context: ["context", "context", "agentdock.context.fleet.v1"],
  task_progress: ["task", "task-progress", "agentdock.task-progress.v1"],
  file_change: ["file", "file-change", "agentdock.file-change.v1"],
  dynamic_mcp: ["dynamic", "dynamic-mcp", "agentdock.dynamic-mcp.v1"],
  artifact: ["artifact", "artifact", "agentdock.artifact.v1"],
  recall: ["recall", "recall", "agentdock.recall.v1"],
  workflow: ["workflow", "workflow", "agentdock.workflow.v1"],
  acp_status: ["acp", "acp-status", "agentdock.acp-status.v1"],
};
const check = process.argv.includes("--check");
const css = (
  await transform(await readFile(path.join(root, "src/style.css"), "utf8"), {
    loader: "css",
    minify: true,
  })
).code;
const manifest = {};
await mkdir(output, { recursive: true });
async function save(name, data) {
  const file = path.join(output, name);
  if (check) {
    if ((await readFile(file, "utf8")) !== data)
      throw new Error(`Generated output differs: ${name}. Run npm run build.`);
  } else await writeFile(file, data);
}
for (const [view, [source, slug, contract]] of Object.entries(views)) {
  const result = await build({
    absWorkingDir: root,
    stdin: {
      contents: `import {start} from './src/bridge'; import {normalize} from './src/views/${source}'; start(${JSON.stringify(view)},normalize);`,
      resolveDir: root,
      sourcefile: `${view}.ts`,
    },
    bundle: true,
    write: false,
    format: "iife",
    platform: "browser",
    target: "es2022",
    minify: true,
    legalComments: "none",
    metafile: true,
    treeShaking: true,
    define: { "process.env.NODE_ENV": '"production"' },
  });
  if (
    Object.keys(result.metafile.inputs).some(
      (p) => p.includes("react-dom") || p.includes("/react/"),
    )
  )
    throw new Error("Unexpected React runtime included");
  const js = result.outputFiles[0].text.replace(/<\/script/gi, "<\\/script");
  const html = `<!doctype html><!-- ${notices} --><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'none'; img-src data:; base-uri 'none'; form-action 'none'"><meta name="agentdock-view" content="${view}"><title>AgentDock</title><style>${css}</style></head><body><main id="content"><p role="status">Connecting…</p></main><script>${js}</script></body></html>\n`;
  const bytes = Buffer.byteLength(html),
    hash = createHash("sha256").update(html).digest("hex");
  if (bytes > 512 * 1024)
    throw new Error(`${view} exceeds 512 KiB standalone HTML budget: ${bytes}`);
  manifest[view] = {
    file: `${view}.html`,
    uri: `ui://agentdock/${slug}/v2-${hash.slice(0, 16)}.html`,
    legacy: `ui://agentdock/${slug}`,
    contract,
    sha256: hash,
    bytes,
    gzipBytes: gzipSync(html).length,
  };
  await save(`${view}.html`, html);
}
await save("manifest.json", JSON.stringify(manifest, null, 2) + "\n");
const expected = new Set([
  ...Object.keys(views).map((v) => `${v}.html`),
  "manifest.json",
]);
for (const file of await readdir(output))
  if (!expected.has(file))
    throw new Error(`Unexpected generated output: ${file}`);
console.log(JSON.stringify(manifest, null, 2));
console.log(
  check
    ? "Generated UI matches source."
    : "Built eight self-contained MCP App views.",
);
