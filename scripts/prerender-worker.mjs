// One disposable browser process per render. All settings arrive over stdin;
// neither service keys nor admin credentials are inherited by the context.
import { pathToFileURL } from "node:url";

const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const input = JSON.parse(Buffer.concat(chunks).toString("utf8"));
const workerStartedAt = Date.now();
const stage = (value) => process.stderr.write(`stage=${value}\n`);
const emitTiming = (phase, startedAt) => {
  const elapsed = Math.max(0, Math.min(300000, Math.floor(Date.now() - startedAt)));
  process.stderr.write(`timing phase=${phase} ms=${elapsed}\n`);
};
const emitElapsedTiming = (phase, elapsedMs) => {
  const elapsed = Math.max(0, Math.min(300000, Math.floor(elapsedMs)));
  process.stderr.write(`timing phase=${phase} ms=${elapsed}\n`);
};
stage("module");
const { chromium } = await import(pathToFileURL(input.playwrightModule).href);
let browser;
let emitRuntimeDataTiming = () => {};
try {
  const browserLaunchStartedAt = Date.now();
  try {
    browser = await chromium.launch({ executablePath: input.browserPath, headless: true, chromiumSandbox: true, args: [`--js-flags=--max-old-space-size=${input.heapMB}`] });
  } finally {
    emitTiming("browser_launch", browserLaunchStartedAt);
  }
  stage("browser");
  const contextStartedAt = Date.now();
  let context;
  let page;
  try {
    context = await browser.newContext({ viewport: { width: input.width, height: input.height }, serviceWorkers: "block", acceptDownloads: false, extraHTTPHeaders: { "X-Engine-Prerender-Worker": input.nonce } });
    page = await context.newPage();
  } finally {
    emitTiming("context_creation", contextStartedAt);
  }
  stage("context");
  page.setDefaultTimeout(input.timeoutMs);
  let lastActivity = Date.now();
  const pending = new Set();
  let forbidden = false;
  let requestCount = 0;
  let transferredBytes = 0;
  let requestFailures = 0;
  let failedResponses = 0;
  let pageErrors = 0;
  let policyBlockedRequests = 0;
  let originBlocks = 0;
  let methodBlocks = 0;
  let pathBlocks = 0;
  let requestLimitBlocks = 0;
  let abortedRequestFailures = 0;
  let failedRequestFailures = 0;
  let blockedByClientFailures = 0;
  let connectionRequestFailures = 0;
  let otherRequestFailures = 0;
  let policyAbortFailures = 0;
  let abortedScripts = 0;
  let abortedStylesheets = 0;
  let abortedFetchXHR = 0;
  let abortedImages = 0;
  let abortedOther = 0;
  const policyAbortedRequests = new WeakSet();
  const incrementCounter = (value) => Math.min(value + 1, 10000);
  const readinessCounters = () => ` request_failures=${requestFailures} failed_responses=${failedResponses} page_errors=${pageErrors} policy_blocked_requests=${policyBlockedRequests} origin_blocks=${originBlocks} method_blocks=${methodBlocks} path_blocks=${pathBlocks} request_limit_blocks=${requestLimitBlocks} request_failure_aborted=${abortedRequestFailures} request_failure_failed=${failedRequestFailures} request_failure_blocked_by_client=${blockedByClientFailures} request_failure_connection=${connectionRequestFailures} request_failure_other=${otherRequestFailures} request_failure_policy_abort=${policyAbortFailures} aborted_script=${abortedScripts} aborted_stylesheet=${abortedStylesheets} aborted_fetch_xhr=${abortedFetchXHR} aborted_image=${abortedImages} aborted_other=${abortedOther}`;
  const protocol = await context.newCDPSession(page);
  await protocol.send("Network.enable");
  protocol.on("Network.dataReceived", ({ encodedDataLength, dataLength }) => {
    transferredBytes += Math.max(encodedDataLength, dataLength);
    if (transferredBytes > input.maxNetworkBytes) {
      forbidden = true;
      void page.close();
    }
  });
  const isRuntimeDataPath = (path) => path === "/api/v2/runtime"
    || /^\/api\/v2\/content\/[^/]+\/records$/.test(path);
  const allowedPath = (path) => isRuntimeDataPath(path)
    || /^\/api\/v2\/site-themes\/snapshots\/[0-9a-f-]+$/.test(path)
    || /^\/api\/v2\/system-assets\/[0-9a-f-]+$/.test(path)
    || path === "/favicon.svg" || path.startsWith("/feature-assets/") || path.startsWith("/assets/") || path.startsWith("/media/");
  // Inspect only same-origin GETs on the fixed runtime/data allowlist. The URL
  // is used for matching and discarded; it, its query, IDs, and response data
  // never enter the diagnostic stream.
  const isRuntimeDataRequest = (request) => {
    if (request.method() !== "GET") return false;
    try {
      const target = new URL(request.url());
      return target.origin === input.origin && isRuntimeDataPath(target.pathname);
    } catch {
      return false;
    }
  };
  const runtimeDataRequests = new Set();
  let runtimeDataStartedAt;
  let runtimeDataCompletedAt;
  let runtimeDataTimingEmitted = false;
  const trackRuntimeDataRequest = (request) => {
    if (!isRuntimeDataRequest(request)) return;
    if (runtimeDataStartedAt === undefined) runtimeDataStartedAt = Date.now();
    runtimeDataRequests.add(request);
  };
  const finishRuntimeDataRequest = (request) => {
    if (runtimeDataRequests.delete(request) && runtimeDataRequests.size === 0) {
      runtimeDataCompletedAt = Date.now();
    }
  };
  emitRuntimeDataTiming = () => {
    if (runtimeDataStartedAt === undefined || runtimeDataTimingEmitted) return;
    const endedAt = runtimeDataRequests.size === 0 && runtimeDataCompletedAt !== undefined
      ? runtimeDataCompletedAt
      : Date.now();
    emitElapsedTiming("runtime_data", endedAt - runtimeDataStartedAt);
    runtimeDataTimingEmitted = true;
  };
  await context.route("**/*", async (route) => {
    const request = route.request();
    requestCount++;
    const target = new URL(request.url());
    const isNavigation = request.isNavigationRequest() && request.frame() === page.mainFrame() && target.href === new URL(input.url).href;
    let blockReason = "";
    if (requestCount > input.maxRequests) blockReason = "request_limit";
    else if (target.origin !== input.origin) blockReason = "origin";
    else if (!["GET", "HEAD"].includes(request.method())) blockReason = "method";
    else if (!isNavigation && !allowedPath(target.pathname)) blockReason = "path";
    if (blockReason) {
      policyBlockedRequests = incrementCounter(policyBlockedRequests);
      if (blockReason === "origin") originBlocks = incrementCounter(originBlocks);
      else if (blockReason === "method") methodBlocks = incrementCounter(methodBlocks);
      else if (blockReason === "path") pathBlocks = incrementCounter(pathBlocks);
      else requestLimitBlocks = incrementCounter(requestLimitBlocks);
      policyAbortedRequests.add(request);
      forbidden = true;
      await route.abort("blockedbyclient");
      return;
    }
    await route.continue();
  });
  await page.routeWebSocket("**/*", (socket) => { forbidden = true; socket.close(); });
  page.on("request", (request) => {
    pending.add(request);
    trackRuntimeDataRequest(request);
    lastActivity = Date.now();
  });
  page.on("response", (response) => {
    if (response.status() >= 400) failedResponses = incrementCounter(failedResponses);
  });
  page.on("pageerror", () => { pageErrors = incrementCounter(pageErrors); });
  const completed = (request) => { pending.delete(request); lastActivity = Date.now(); };
  page.on("requestfinished", (request) => {
    finishRuntimeDataRequest(request);
    completed(request);
  });
  page.on("requestfailed", (request) => {
    requestFailures = incrementCounter(requestFailures);
    if (policyAbortedRequests.has(request)) policyAbortFailures = incrementCounter(policyAbortFailures);
    else {
      const failure = request.failure()?.errorText;
      if (failure === "net::ERR_ABORTED") {
        abortedRequestFailures = incrementCounter(abortedRequestFailures);
        const resourceType = request.resourceType();
        if (resourceType === "script") abortedScripts = incrementCounter(abortedScripts);
        else if (resourceType === "stylesheet") abortedStylesheets = incrementCounter(abortedStylesheets);
        else if (resourceType === "fetch" || resourceType === "xhr") abortedFetchXHR = incrementCounter(abortedFetchXHR);
        else if (resourceType === "image") abortedImages = incrementCounter(abortedImages);
        else abortedOther = incrementCounter(abortedOther);
      }
      else if (failure === "net::ERR_FAILED") failedRequestFailures = incrementCounter(failedRequestFailures);
      else if (failure === "net::ERR_BLOCKED_BY_CLIENT") blockedByClientFailures = incrementCounter(blockedByClientFailures);
      else if (["net::ERR_CONNECTION_REFUSED", "net::ERR_CONNECTION_RESET", "net::ERR_CONNECTION_CLOSED", "net::ERR_TIMED_OUT"].includes(failure)) connectionRequestFailures = incrementCounter(connectionRequestFailures);
      else otherRequestFailures = incrementCounter(otherRequestFailures);
    }
    finishRuntimeDataRequest(request);
    completed(request);
  });
  const navigationStartedAt = Date.now();
  let response;
  try {
    response = await page.goto(input.url, { waitUntil: "domcontentloaded", timeout: input.timeoutMs });
  } finally {
    emitTiming("navigation", navigationStartedAt);
  }
  if (!response || response.status() !== input.status) {
    stage("navigation_status_mismatch");
    throw new Error("navigation status mismatch");
  }
  stage("navigation");
  // Live visits defer feature modules whose placement roots are outside the
  // first viewport. During a Chromium render pass, traverse every root so the
  // captured HTML still contains fully rendered placements before readiness is
  // accepted and cached.
  await page.waitForFunction(() => {
    const root = document.getElementById("root");
    return Boolean(root && !root.hidden && !document.getElementById("prerender-root"));
  }, { timeout: input.timeoutMs });
  await page.evaluate(async () => {
    const roots = [...document.querySelectorAll("[data-engine-placement]")];
    for (const root of roots) {
      root.scrollIntoView({ block: "center", inline: "nearest" });
      await new Promise((resolve) => requestAnimationFrame(() => resolve()));
    }
    window.scrollTo(0, 0);
  });
  const safetyMarginMs = Math.min(1500, Math.max(250, Math.floor(input.timeoutMs * 0.1)));
  const readinessTimeoutMs = input.timeoutMs - (Date.now() - workerStartedAt) - safetyMarginMs;
  const readinessStartedAt = Date.now();
  if (readinessTimeoutMs <= 0) {
    let readiness;
    try {
      readiness = await page.evaluate(({ snapshotId, contentRevision }) => {
        const composition = document.querySelector("[data-snapshot]");
        const root = document.getElementById("root");
        return {
          snapshotMatch: composition?.getAttribute("data-snapshot") === snapshotId,
          revisionMatch: Number(composition?.getAttribute("data-content-revision")) === contentRevision,
          engineReady: document.documentElement.dataset.engineReady === "true",
          placements: root?.querySelectorAll("[data-engine-placement]").length ?? 0,
          ready: root?.querySelectorAll("[data-runtime-feature-ready]").length ?? 0,
          errors: root?.querySelectorAll(".runtime-feature-error").length ?? 0,
        };
      }, { snapshotId: input.snapshotId, contentRevision: input.contentRevision });
    } catch {
      readiness = { snapshotMatch: false, revisionMatch: false, engineReady: false, placements: 0, ready: 0, errors: 0 };
    }
    stage("readiness_budget_exhausted");
    process.stderr.write("readiness snapshot_match=" + Boolean(readiness.snapshotMatch)
      + " revision_match=" + Boolean(readiness.revisionMatch)
      + " engine_ready=" + Boolean(readiness.engineReady)
      + " placements=" + readiness.placements
      + " ready=" + readiness.ready
      + " errors=" + readiness.errors + readinessCounters() + "\n");
    emitTiming("readiness", readinessStartedAt);
    throw new Error("runtime readiness budget exhausted");
  }
  try {
    stage("readiness_wait");
    await page.waitForFunction(({ snapshotId, contentRevision }) => {
      const composition = document.querySelector("[data-snapshot]");
      return composition?.getAttribute("data-snapshot") === snapshotId
        && Number(composition.getAttribute("data-content-revision")) === contentRevision
        && document.documentElement.dataset.engineReady === "true";
    }, { snapshotId: input.snapshotId, contentRevision: input.contentRevision }, { timeout: readinessTimeoutMs });
  } catch {
    // Report only fixed booleans and element counts. Never include page text,
    // URLs, feature identifiers, exception messages, or browser stacks.
    let readiness;
    try {
      readiness = await page.evaluate(({ snapshotId, contentRevision }) => {
        const composition = document.querySelector("[data-snapshot]");
        const root = document.getElementById("root");
        return {
          snapshotMatch: composition?.getAttribute("data-snapshot") === snapshotId,
          revisionMatch: Number(composition?.getAttribute("data-content-revision")) === contentRevision,
          engineReady: document.documentElement.dataset.engineReady === "true",
          placements: root?.querySelectorAll("[data-engine-placement]").length ?? 0,
          ready: root?.querySelectorAll("[data-runtime-feature-ready]").length ?? 0,
          errors: root?.querySelectorAll(".runtime-feature-error").length ?? 0,
        };
      }, { snapshotId: input.snapshotId, contentRevision: input.contentRevision });
    } catch {
      // Keep deterministic, bounded diagnostics if the page was already closed.
      readiness = { snapshotMatch: false, revisionMatch: false, engineReady: false, placements: 0, ready: 0, errors: 0 };
    }
    stage("readiness_timeout");
    process.stderr.write(`readiness snapshot_match=${Boolean(readiness.snapshotMatch)} revision_match=${Boolean(readiness.revisionMatch)} engine_ready=${Boolean(readiness.engineReady)} placements=${readiness.placements} ready=${readiness.ready} errors=${readiness.errors}${readinessCounters()}\n`);
    emitTiming("readiness", readinessStartedAt);
    throw new Error("runtime readiness timed out");
  }
  let readySnapshot;
  try {
    readySnapshot = await page.evaluate(({ snapshotId, contentRevision }) => {
      const composition = document.querySelector("[data-snapshot]");
      const root = document.getElementById("root");
      return {
        snapshotMatch: composition?.getAttribute("data-snapshot") === snapshotId,
        revisionMatch: Number(composition?.getAttribute("data-content-revision")) === contentRevision,
        engineReady: document.documentElement.dataset.engineReady === "true",
        placements: root?.querySelectorAll("[data-engine-placement]").length ?? 0,
        ready: root?.querySelectorAll("[data-runtime-feature-ready]").length ?? 0,
        errors: root?.querySelectorAll(".runtime-feature-error").length ?? 0,
      };
    }, { snapshotId: input.snapshotId, contentRevision: input.contentRevision });
  } catch {
    readySnapshot = { snapshotMatch: false, revisionMatch: false, engineReady: false, placements: 0, ready: 0, errors: 0 };
  }
  stage("ready");
  process.stderr.write(`readiness snapshot_match=${Boolean(readySnapshot.snapshotMatch)} revision_match=${Boolean(readySnapshot.revisionMatch)} engine_ready=${Boolean(readySnapshot.engineReady)} placements=${readySnapshot.placements} ready=${readySnapshot.ready} errors=${readySnapshot.errors}${readinessCounters()}\n`);
  const deadline = Date.now() + input.timeoutMs;
  while (pending.size > 0 || Date.now() - lastActivity < input.quietMs) {
    if (Date.now() > deadline) {
      emitTiming("readiness", readinessStartedAt);
      throw new Error("page did not settle");
    }
    await new Promise((resolve) => setTimeout(resolve, Math.min(input.quietMs, 50)));
  }
  let featureErrorCount = 0;
  try {
    featureErrorCount = await page.locator(".runtime-feature-error").count();
  } catch {
    // A policy failure may already have closed the page; keep diagnostics fixed.
  }
  emitTiming("readiness", readinessStartedAt);
  if (forbidden) throw new Error("feature attempted a blocked network operation");
  if (featureErrorCount > 0) throw new Error("feature rendering failed");
  stage("settled");
  const serializationStartedAt = Date.now();
  try {
    // The worker has already fetched the feature stylesheets to render the
    // page. Carry small, self-contained same-origin styles into the cached
    // HTML so the real browser can paint the prerender without another style
    // request, and the live mount can recognize them as ready. Keep linked
    // stylesheets that depend on external resources or are too large.
    await page.evaluate(() => {
      const maxInlineBytes = 96 * 1024;
      let inlineBytes = 0;
      for (const link of document.querySelectorAll('link[data-feature-style][data-loaded="true"]')) {
        if (!(link instanceof HTMLLinkElement) || !link.sheet || link.href === "") continue;
        const stylesheetURL = new URL(link.href);
        if (stylesheetURL.origin !== location.origin || !["http:", "https:"].includes(stylesheetURL.protocol)) continue;
        let css = "";
        try {
          css = [...link.sheet.cssRules].map((rule) => rule.cssText).join("\n");
        } catch {
          continue;
        }
        // Relative assets must keep resolving against their stylesheet URL;
        // imported styles need their own fetch and cascade order as well.
        if (/@import\b|url\s*\(/i.test(css) || /<\/style/i.test(css)) continue;
        const bytes = new TextEncoder().encode(css).byteLength;
        if (bytes === 0 || inlineBytes + bytes > maxInlineBytes) continue;
        const style = document.createElement("style");
        style.dataset.featureStyle = link.dataset.featureStyle || link.href;
        style.dataset.loaded = "true";
        if (link.media) style.media = link.media;
        style.textContent = css;
        link.replaceWith(style);
        inlineBytes += bytes;
      }
    });
    // Keep the public markup available without JS while the separate live mount
    // loads. Static forms cannot submit before the runtime has become ready.
    await page.evaluate(() => {
      const root = document.getElementById("root");
      if (!root) throw new Error("runtime root missing");
      // The live render now contains every static HTML placement, so keep only
      // one copy in the serialized document. If prerendering fails, the server
      // response retains its readable static fallback.
      document.getElementById("engine-static-feature-fallback")?.remove();
      // Keep the populated document tree under #root in the serialized
      // response. The runtime renames it only after JS starts so no-JS clients
      // and crawlers retain the canonical root and server-rendered markup.
      root.querySelectorAll("form").forEach((form) => { form.setAttribute("inert", ""); });
      const live = document.createElement("div");
      live.id = "engine-live-root";
      live.hidden = true;
      root.after(live);
      document.documentElement.removeAttribute("data-engine-ready");
    });
    const html = `<!DOCTYPE html>\n${await page.locator("html").evaluate((element) => element.outerHTML)}`;
    if (Buffer.byteLength(html, "utf8") > input.maxHTMLBytes) throw new Error("HTML size limit exceeded");
    stage("serialized");
    process.stdout.write(html);
  } finally {
    emitTiming("serialization", serializationStartedAt);
  }
} catch {
  // Error messages may contain page data, URLs, or browser diagnostics.
  // The fixed stage and normalized readiness counters are sufficient.
  process.exitCode = 1;
} finally {
  emitRuntimeDataTiming();
  await browser?.close();
}
