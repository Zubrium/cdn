import { createHash, randomUUID } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";

const adminURL = required("E2E_ADMIN_URL").replace(/\/+$/, "");
const publicURL = required("E2E_PUBLIC_URL").replace(/\/+$/, "");
const credential = required("E2E_BOOTSTRAP_CREDENTIAL");
const runId = required("E2E_RUN_ID");
const statePath = required("E2E_STATE_FILE");
const mode = process.argv[2];
if (mode !== "prepare" && mode !== "fixtures") {
  throw new Error("usage: setup.mjs prepare|fixtures");
}

function required(name) {
  const value = process.env[name];
  if (!value || !value.trim()) throw new Error(`missing ${name}`);
  return value;
}

function key(kind) {
  return `e2e-${runId}-${kind}`.replace(/[^A-Za-z0-9._:-]/g, "-").slice(0, 128);
}

function sha256(data) {
  return createHash("sha256").update(data).digest("hex");
}

class AdminClient {
  constructor() {
    this.origin = adminURL;
    this.cookies = new Map();
    this.csrf = "";
  }

  async request(method, path, options = {}) {
    const url = new URL(path, this.origin);
    const headers = { Origin: this.origin, ...(options.headers ?? {}) };
    if (this.cookies.size > 0) {
      headers.Cookie = [...this.cookies].map(([name, value]) => `${name}=${value}`).join("; ");
    }
    if (this.csrf) headers["X-CSRF-Token"] = this.csrf;
    let body;
    if (options.body !== undefined) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(options.body);
    } else if (options.raw !== undefined) {
      body = options.raw;
    }
    const response = await fetch(url, { method, headers, body });
    for (const value of response.headers.getSetCookie?.() ?? []) {
      const pair = value.split(";", 1)[0];
      const index = pair.indexOf("=");
      if (index > 0) this.cookies.set(pair.slice(0, index), pair.slice(index + 1));
    }
    const text = await response.text();
    let payload = text;
    if ((response.headers.get("content-type") ?? "").includes("application/json") && text) {
      try {
        payload = JSON.parse(text);
      } catch {
        // Keep raw text for better diagnostics.
      }
    }
    if (!response.ok) {
      const detail = typeof payload === "string" ? payload.slice(0, 300) : JSON.stringify(payload).slice(0, 300);
      throw new Error(`${method} ${url.pathname} returned ${response.status}: ${detail}`);
    }
    return { status: response.status, headers: response.headers, body: payload };
  }

  async login() {
    const response = await this.request("POST", "/api/admin/session", {
      body: { credential },
      headers: { "Content-Type": "application/json" },
    });
    if (!response.body?.principal) throw new Error("admin login returned no principal");
    this.csrf = this.cookies.get("zubrium_admin_csrf") ?? "";
    if (!this.csrf) throw new Error("admin login returned no CSRF cookie");
    return this;
  }

  async json(method, path, body, extraHeaders) {
    const response = await this.request(method, path, { body, headers: extraHeaders });
    return response.body;
  }

  async poll(path, predicate, label, timeoutMs = 180_000) {
    const deadline = Date.now() + timeoutMs;
    let last;
    while (Date.now() < deadline) {
      last = await this.json("GET", path);
      if (predicate(last)) return last;
      await new Promise((resolve) => setTimeout(resolve, 1_000));
    }
    throw new Error(`${label} timed out: ${JSON.stringify(last).slice(0, 400)}`);
  }
}

const crcTable = (() => {
  const table = new Uint32Array(256);
  for (let index = 0; index < 256; index += 1) {
    let value = index;
    for (let bit = 0; bit < 8; bit += 1) {
      value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
    }
    table[index] = value >>> 0;
  }
  return table;
})();

function crc32(buffer) {
  let value = 0xffffffff;
  for (const byte of buffer) value = crcTable[(value ^ byte) & 0xff] ^ (value >>> 8);
  return (value ^ 0xffffffff) >>> 0;
}

function makeZip(entries) {
  const locals = [];
  const centrals = [];
  let offset = 0;
  for (const [name, content] of entries) {
    const nameBuffer = Buffer.from(name, "utf8");
    const data = Buffer.from(content, "utf8");
    const checksum = crc32(data);
    const local = Buffer.alloc(30 + nameBuffer.length);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0, 6);
    local.writeUInt16LE(0, 8);
    local.writeUInt16LE(0, 10);
    local.writeUInt16LE(0x0021, 12);
    local.writeUInt32LE(checksum, 14);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(nameBuffer.length, 26);
    local.writeUInt16LE(0, 28);
    nameBuffer.copy(local, 30);
    locals.push(local, data);

    const central = Buffer.alloc(46 + nameBuffer.length);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(20, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(0, 8);
    central.writeUInt16LE(0, 10);
    central.writeUInt16LE(0, 12);
    central.writeUInt16LE(0x0021, 14);
    central.writeUInt32LE(checksum, 16);
    central.writeUInt32LE(data.length, 20);
    central.writeUInt32LE(data.length, 24);
    central.writeUInt16LE(nameBuffer.length, 28);
    central.writeUInt16LE(0, 30);
    central.writeUInt16LE(0, 32);
    central.writeUInt16LE(0, 34);
    central.writeUInt16LE(0, 36);
    central.writeUInt32LE(0, 38);
    central.writeUInt32LE(offset, 42);
    nameBuffer.copy(central, 46);
    centrals.push(central);
    offset += local.length + data.length;
  }
  const centralSize = centrals.reduce((sum, entry) => sum + entry.length, 0);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(0, 4);
  end.writeUInt16LE(0, 6);
  end.writeUInt16LE(centrals.length, 8);
  end.writeUInt16LE(centrals.length, 10);
  end.writeUInt32LE(centralSize, 12);
  end.writeUInt32LE(offset, 16);
  end.writeUInt16LE(0, 20);
  return Buffer.concat([...locals, ...centrals, end]);
}

async function readState() {
  return JSON.parse(await readFile(statePath, "utf8"));
}

async function writeState(state) {
  await writeFile(statePath, `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
}

async function uploadFile(client, bytes) {
  const digest = sha256(bytes);
  const begin = await client.json(
    "POST",
    "/api/files/uploads",
    {
      name: "e2e-feature.zip",
      contentType: "application/zip",
      size: bytes.length,
      sha256: digest,
      groups: ["features"],
    },
    { "Idempotency-Key": key("feature-upload") },
  );
  if (begin.state === "committed" && begin.fileId) return { id: begin.fileId, digest };
  let session = begin;
  const partSize = session.partSize;
  for (let index = 0; index * partSize < bytes.length; index += 1) {
    const part = bytes.subarray(index * partSize, (index + 1) * partSize);
    session = await client.request(
      "PUT",
      `/api/files/uploads/${encodeURIComponent(session.id)}/parts/${index + 1}`,
      {
        raw: part,
        headers: {
          "Content-Type": "application/octet-stream",
          "X-Content-SHA256": sha256(part),
        },
      },
    ).then((response) => response.body);
  }
  const committed = await client.request(
    "POST",
    `/api/files/uploads/${encodeURIComponent(session.id)}/commit`,
    {},
  );
  return { id: committed.body.id, digest };
}

async function publishWorkspace(client, snapshotID, locale) {
  const workspace = await client.json(
    "GET",
    `/api/admin/content/snapshots/${encodeURIComponent(snapshotID)}/workspace?locale=${encodeURIComponent(locale)}`,
  );
  await client.json(
    "POST",
    `/api/admin/content/snapshots/${encodeURIComponent(snapshotID)}/workspace/publish?locale=${encodeURIComponent(locale)}`,
    {
      expected_revision: workspace.revision,
      expected_selection_revision: workspace.selection_revision,
    },
    { "Idempotency-Key": key(`prepare-publish-${locale}`) },
  );
}

async function buildArtifact(client, snapshotID, locale) {
  const job = await client.json(
    "POST",
    "/api/admin/builds",
    { snapshot_id: snapshotID, locale },
    { "Idempotency-Key": key(`build-${locale}`) },
  );
  const ready = await client.poll(
    `/api/admin/builds/${encodeURIComponent(job.id)}`,
    (value) => value.state === "ready" || value.state === "failed",
    `build ${locale}`,
  );
  if (ready.state !== "ready" || !ready.artifact_manifest_file_id) {
    throw new Error(`build ${locale} failed: ${JSON.stringify(ready).slice(0, 400)}`);
  }
  return ready.artifact_manifest_file_id;
}

const client = await new AdminClient().login();

if (mode === "prepare") {
  await client.json(
    "PUT",
    "/api/admin/content/locales",
    {
      expected_revision: 0,
      locales: ["en-US", "de-DE"],
      default_locale: "en-US",
    },
    { "Idempotency-Key": key("locales") },
  );
  const fileSet = await client.json("POST", "/api/files/sets", {
    namespace: "e2e",
    name: `e2e-features-${runId}`,
    managementGroup: "features",
  });
  await writeState({ runId, fileSetID: fileSet.id });
  process.stdout.write(`E2E prepare complete (run ${runId})\n`);
} else {
  const state = await readState();
  const featureKey = "e2e-page";
  const featureVersion = "1.0.0";
  const manifest = {
    formatVersion: 2,
    key: featureKey,
    name: "E2E Page",
    version: featureVersion,
    engineApi: "v1",
    slots: ["main"],
    arguments: {},
    dataInputs: [],
    runtime: { abi: "static-lifecycle", entry: "assets/app.js", styles: [] },
  };
  const archive = makeZip([
    ["manifest.json", `${JSON.stringify(manifest)}\n`],
    ["assets/app.js", "export function render() { return 'e2e'; }\n"],
  ]);
  const uploaded = await uploadFile(client, archive);

  const importJob = await client.json(
    "POST",
    "/api/admin/feature-imports",
    { source_file_id: uploaded.id, expected_sha256: uploaded.digest },
    { "Idempotency-Key": key("feature-import") },
  );
  const imported = await client.poll(
    `/api/admin/feature-imports/${encodeURIComponent(importJob.id)}`,
    (value) => value.state === "ready" || value.state === "succeeded" || value.state === "failed" || value.state === "cancelled",
    "feature import",
  );
  if (imported.state !== "succeeded" && imported.state !== "ready") {
    throw new Error(`feature import failed: ${JSON.stringify(imported).slice(0, 400)}`);
  }

  const page = await client.json(
    "POST",
    "/api/admin/pages",
    { name: `E2E ${runId}` },
    { "Idempotency-Key": key("page") },
  );
  const parent = await client.json(
    "GET",
    `/api/admin/snapshots/${encodeURIComponent(page.latest_snapshot_id)}`,
  );
  const placementID = "e2e-placement";
  const child = await client.json(
    "POST",
    "/api/admin/snapshots",
    {
      page_id: page.id,
      parent_id: parent.id,
      name: "E2E configured snapshot",
      expected_revision: parent.revision,
      expected_content_revision: parent.content_revision ?? 0,
      payload: {
        config: {
          format_version: 3,
          page: {
            id: page.id,
            name: `E2E ${runId}`,
            placements: [
              {
                id: placementID,
                slot: "main",
                build: { featureKey, version: featureVersion },
                arguments: {},
                dataBindings: [],
              },
            ],
          },
        },
        metadata: {},
      },
    },
    { "Idempotency-Key": key("snapshot") },
  );
  const snapshot = await client.poll(
    `/api/admin/snapshots/${encodeURIComponent(child.id)}`,
    (value) => value.state === "ready" || value.state === "failed",
    "snapshot",
  );
  if (snapshot.state !== "ready") {
    throw new Error(`snapshot failed: ${JSON.stringify(snapshot).slice(0, 400)}`);
  }

  for (const locale of ["en-US", "de-DE"]) await publishWorkspace(client, snapshot.id, locale);

  const artifacts = {};
  for (const locale of ["en-US", "de-DE"]) {
    artifacts[locale] = await buildArtifact(client, snapshot.id, locale);
  }

  const routes = [];
  for (const suffix of ["a", "b"]) {
    const route = await client.json(
      "POST",
      "/api/admin/router/routes",
      {
        host: "",
        path: `/e2e-${runId}-${suffix}`,
        match: "exact",
        target: {
          kind: "static-site-artifact",
          default_locale: "en-US",
          locale_targets: [
            { locale: "en-US", artifact_manifest_file_id: artifacts["en-US"] },
            { locale: "de-DE", artifact_manifest_file_id: artifacts["de-DE"] },
          ],
        },
      },
      { "Idempotency-Key": key(`route-${suffix}`) },
    );
    routes.push({
      id: route.id,
      path: route.path,
      initial: { "en-US": artifacts["en-US"], "de-DE": artifacts["de-DE"] },
    });
  }

  const form = await client.json("PUT", "/api/admin/forms/schemas", {
    key: `e2e-${runId}`,
    fields: { message: { type: "text", required: true, min_length: 1, max_length: 200 } },
    expected_revision: 0,
  });

  await writeState({
    ...state,
    pageID: page.id,
    snapshotID: snapshot.id,
    sourceFileID: uploaded.id,
    featureKey,
    featureVersion,
    formSchemaID: form.id,
    locales: ["en-US", "de-DE"],
    routes,
  });
  process.stdout.write(`E2E fixtures complete (run ${runId})\n`);
}
