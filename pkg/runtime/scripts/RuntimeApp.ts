import { isComposition, readRuntimeBootstrap } from "./runtime-bootstrap";
import { ensureStyles } from "./feature-assets";
import {
  applyPageIdentity,
  configurePlacementRoot,
  findOrCreateMain,
  markDocumentReady,
  normalizePlacementRoots,
  renderState,
  setHeadMetadata,
} from "./document-dom";
import { identityFor, mountPlacement, routeFor, updatePlacement } from "./feature-lifecycle";
import type { Composition, MountedFeature } from "./runtime-types";

export function mountRuntime(root: HTMLElement): () => void {
  const bootstrap = readRuntimeBootstrap();
  const mounted = new Map<string, MountedFeature>();
  const pendingMounts = new Map<string, { run: number; controller: AbortController; promise: Promise<MountedFeature> }>();
  const deferredPlacementIds = new Set<string>();
  let deferredObserver: IntersectionObserver | null = null;
  let composition: Composition | null = null;
  let path = window.location.pathname || "/";
  let search = window.location.search.slice(1);
  let generation = 0;
  let disposed = false;

  const unmountAll = () => {
    deferredObserver?.disconnect();
    deferredObserver = null;
    deferredPlacementIds.clear();
    for (const pending of pendingMounts.values()) pending.controller.abort();
    pendingMounts.clear();
    for (const item of mounted.values()) {
      item.controller.abort();
      void Promise.resolve(item.instance?.unmount()).catch(() => undefined);
    }
    mounted.clear();
  };

  const updateReady = (run: number) => {
    const allReady = composition !== null
      && mounted.size === composition.placements.length
      && deferredPlacementIds.size === 0
      && pendingMounts.size === 0
      && [...mounted.values()].every((item) => item.ready && !item.failed)
      && !root.querySelector(".runtime-loading, .runtime-feature-error");
    markDocumentReady(root, run, () => generation, allReady);
  };

  const reportError = (run: number, error: unknown) => {
    if (disposed || run !== generation) return;
    console.error("Public feature failed", error);
    updateReady(run);
    root.hidden = false;
    document.getElementById("prerender-root")?.remove();
  };

  const startPlacement = async (
    placement: Composition["placements"][number],
    placementRoot: HTMLDivElement,
    run: number,
    route: ReturnType<typeof routeFor>,
    systemAssets: Composition["systemAssets"],
  ) => {
    const controller = new AbortController();
    const promise = mountPlacement(placement, placementRoot, controller, route, systemAssets, (error) => reportError(run, error));
    const request = { run, controller, promise };
    pendingMounts.set(placement.id, request);
    const item = await promise;
    if (pendingMounts.get(placement.id) === request) pendingMounts.delete(placement.id);
    if (run !== generation || disposed) {
      item.controller.abort();
      await Promise.resolve(item.instance?.unmount()).catch(() => undefined);
      return;
    }
    mounted.set(placement.id, item);
    updateReady(run);
  };

  const renderComposition = async (next: Composition, expectedGeneration?: number, deferOffscreen = false) => {
    if (disposed) return;
    const run = expectedGeneration ?? generation + 1;
    generation = run;
    deferredObserver?.disconnect();
    deferredObserver = null;
    deferredPlacementIds.clear();
    for (const pending of pendingMounts.values()) pending.controller.abort();
    pendingMounts.clear();
    composition = next;
    applyPageIdentity(root, next);
    setHeadMetadata(next.metadata);
    let roots: Map<string, HTMLDivElement>;
    try {
      const main = findOrCreateMain(root);
      roots = normalizePlacementRoots(root, next, main);
    } catch (error) {
      unmountAll();
      composition = null;
      renderState(root, "Сайт временно недоступен", error instanceof Error ? error.message : "Разметка страницы повреждена.");
      markDocumentReady(root, run, () => generation, true);
      return;
    }
    const nextIds = new Set(next.placements.map((placement) => placement.id));
    for (const [id, item] of mounted) {
      if (nextIds.has(id)) continue;
      item.controller.abort();
      void Promise.resolve(item.instance?.unmount()).catch(() => undefined);
      mounted.delete(id);
    }
    const route = routeFor(path, search, next);
    const deferredRoots = new Map<string, { placement: Composition["placements"][number]; root: HTMLDivElement }>();
    const eagerTasks: Promise<void>[] = [];
    const canDeferOffscreen = deferOffscreen && typeof IntersectionObserver !== "undefined";
    for (const placement of next.placements) {
      const placementRoot = roots.get(placement.id)!;
      configurePlacementRoot(placementRoot, placement);
      const identity = identityFor(placement);
      const existing = mounted.get(placement.id);
      if (existing && existing.identity !== identity) {
        existing.controller.abort();
        await Promise.resolve(existing.instance?.unmount()).catch(() => undefined);
        mounted.delete(placement.id);
      }
      const current = mounted.get(placement.id);
      if (current) {
        eagerTasks.push(updatePlacement(current, placement, route, next.systemAssets, (error) => reportError(run, error)));
        continue;
      }
      const bounds = placementRoot.getBoundingClientRect();
      const isNearViewport = bounds.bottom > -250 && bounds.top < window.innerHeight + 250;
      if (canDeferOffscreen && !isNearViewport) {
        deferredPlacementIds.add(placement.id);
        deferredRoots.set(placement.id, { placement, root: placementRoot });
        continue;
      }
      eagerTasks.push(startPlacement(placement, placementRoot, run, route, next.systemAssets));
    }
    await Promise.all(eagerTasks);
    if (run !== generation || disposed) return;
    if (deferredRoots.size > 0) {
      deferredObserver = new IntersectionObserver((entries) => {
        if (run !== generation || disposed) return;
        for (const entry of entries) {
          if (!entry.isIntersecting) continue;
          const id = (entry.target as HTMLElement).dataset.enginePlacement;
          if (!id) continue;
          const deferred = deferredRoots.get(id);
          if (!deferred) continue;
          deferredRoots.delete(id);
          deferredPlacementIds.delete(id);
          deferredObserver?.unobserve(entry.target);
          void startPlacement(deferred.placement, deferred.root, run, route, next.systemAssets);
        }
      }, { rootMargin: "250px 0px" });
      for (const { root: placementRoot } of deferredRoots.values()) deferredObserver.observe(placementRoot);
    }
    const allReady = mounted.size === next.placements.length
      && deferredPlacementIds.size === 0
      && pendingMounts.size === 0
      && [...mounted.values()].every((item) => item.ready && !item.failed)
      && !root.querySelector(".runtime-loading, .runtime-feature-error");
    markDocumentReady(root, run, () => generation, allReady, deferOffscreen);
  };

  const loadRoute = async () => {
    if (disposed) return;
    const run = ++generation;
    try {
      const params = new URLSearchParams({ path: path || "/", search });
      const response = await fetch(`/api/v2/runtime?${params.toString()}`, { headers: { Accept: "application/json" } });
      if (run !== generation || disposed) return;
      if (response.status === 404) {
        unmountAll();
        composition = null;
        renderState(root, "Страница не найдена", "Проверьте адрес или вернитесь на главную.");
        markDocumentReady(root, run, () => generation, true);
        return;
      }
      if (response.status === 503) {
        unmountAll();
        composition = null;
        renderState(root, "Сайт ещё не настроен", "Для этой ветки пока не назначен снапшот сайта.");
        markDocumentReady(root, run, () => generation, true);
        return;
      }
      if (!response.ok) {
        const body = await response.json().catch(() => null) as { error?: { message?: string }; message?: string } | null;
        throw new Error(body?.error?.message ?? body?.message ?? `Сервер ответил с кодом ${response.status}.`);
      }
      const value: unknown = await response.json();
      if (!isComposition(value)) throw new Error("Сервер вернул данные страницы в неверном формате.");
      await ensureStyles([...(value.sharedStyleUrls ?? []), ...(value.themeStyleUrl ? [value.themeStyleUrl] : [])]);
      if (run !== generation || disposed) return;
      await renderComposition(value, run);
    } catch (error) {
      if (run !== generation || disposed) return;
      unmountAll();
      composition = null;
      renderState(root, "Сайт временно недоступен", error instanceof Error ? error.message : "Проверьте соединение и попробуйте снова.", () => { void loadRoute(); });
      markDocumentReady(root, run, () => generation, true);
    }
  };

  const onPopState = () => {
    path = window.location.pathname || "/";
    search = window.location.search.slice(1);
    void loadRoute();
  };
  window.addEventListener("popstate", onPopState);

  if (!bootstrap.composition) {
    renderState(root, "Сайт временно недоступен", bootstrap.error ?? "Не удалось запустить страницу.");
    document.documentElement.dataset.engineReady = "false";
    root.hidden = false;
    document.getElementById("prerender-root")?.remove();
  } else {
    const initialGeneration = generation;
    void ensureStyles([...(bootstrap.composition.sharedStyleUrls ?? []), ...(bootstrap.composition.themeStyleUrl ? [bootstrap.composition.themeStyleUrl] : [])])
      .then(async () => {
        if (disposed || generation !== initialGeneration) return;
        // The Go document already contains the Chromium-prerendered feature
        // markup. Give the browser one paint opportunity before the existing
        // ABI mounts replace that markup with interactive component trees.
        await new Promise<void>((resolve) => {
          window.requestAnimationFrame(() => window.requestAnimationFrame(() => resolve()));
        });
        if (!disposed && generation === initialGeneration) return renderComposition(bootstrap.composition!, undefined, true);
      })
      .catch((error: unknown) => {
        if (disposed || generation !== initialGeneration) return;
        composition = null;
        renderState(root, "Сайт временно недоступен", error instanceof Error ? error.message : "Не удалось загрузить стили страницы.");
        document.documentElement.dataset.engineReady = "false";
        root.hidden = false;
        document.getElementById("prerender-root")?.remove();
      });
  }

  return () => {
    disposed = true;
    generation++;
    window.removeEventListener("popstate", onPopState);
    unmountAll();
  };
}
