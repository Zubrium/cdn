// Run inside the image with the production seccomp profile and no network.
import http from "node:http";
import { spawn } from "node:child_process";

const snapshotId = "11111111-1111-1111-1111-111111111111";
const server = http.createServer((_request, response) => {
  response.setHeader("content-type", "text/html; charset=utf-8");
  response.end(`<!DOCTYPE html><html data-engine-ready="true"><body><div id="root"><main data-snapshot="${snapshotId}" data-content-revision="1"><h1>Пререндер работает</h1><form><button>Отправить</button></form></main></div></body></html>`);
});
await new Promise((resolve) => server.listen(39091, "127.0.0.1", resolve));
const child = spawn("/usr/bin/node", ["/app/scripts/prerender-worker.mjs"]);
let output = "";
let errors = "";
child.stdout.on("data", (chunk) => { output += chunk; });
child.stderr.on("data", (chunk) => { errors += chunk; });
child.stdin.end(JSON.stringify({
  playwrightModule: "/opt/playwright/node_modules/playwright/index.mjs",
  browserPath: "/usr/bin/chromium", heapMB: 128, width: 1440, height: 1000,
  timeoutMs: 10000, quietMs: 100, maxRequests: 20, maxNetworkBytes: 1048576,
  url: "http://127.0.0.1:39091/", origin: "http://127.0.0.1:39091",
  snapshotId, contentRevision: 1, status: 200, maxHTMLBytes: 1048576, nonce: "smoke",
}));
const code = await new Promise((resolve) => child.on("close", resolve));
server.close();
if (code || !output.includes("Пререндер работает") || !output.includes('inert=""') || !output.includes("prerender-root")) {
  console.error(errors || output.slice(0, 1500));
  process.exit(1);
}
console.log("Worker produced static HTML and an inert form with Chromium sandbox enabled");
