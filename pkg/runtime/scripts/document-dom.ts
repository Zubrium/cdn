import type { Composition, RuntimePlacement } from "./runtime-types";

const slotOrder = ["background", "header", "main", "footer"] as const;

export function setHeadMetadata(metadata: Composition["metadata"]) {
  document.title = metadata.title;
  if (metadata.language) document.documentElement.lang = metadata.language;
  let canonical = document.head.querySelector<HTMLLinkElement>('link[rel="canonical"]');
  if (!metadata.canonicalUrl) canonical?.remove();
  else {
    if (!canonical) { canonical = document.createElement("link"); canonical.rel = "canonical"; document.head.append(canonical); }
    canonical.href = metadata.canonicalUrl;
  }
  const setMeta = (selector: string, attribute: "name" | "property", key: string, content: string) => {
    let element = document.head.querySelector<HTMLMetaElement>(selector);
    if (!content) { element?.remove(); return; }
    if (!element) {
      element = document.createElement("meta");
      element.setAttribute(attribute, key);
      document.head.append(element);
    }
    element.content = content;
  };
  setMeta('meta[name="robots"]', "name", "robots", metadata.robots ?? "");
  setMeta('meta[name="description"]', "name", "description", metadata.description);
  setMeta('meta[property="og:title"]', "property", "og:title", metadata.ogTitle);
  setMeta('meta[property="og:description"]', "property", "og:description", metadata.ogDescription);
  setMeta('meta[property="og:image"]', "property", "og:image", metadata.ogImageUrl);
  let icon = document.head.querySelector<HTMLLinkElement>('link[rel="icon"]');
  if (!icon) { icon = document.createElement("link"); icon.rel = "icon"; document.head.append(icon); }
  icon.href = metadata.faviconUrl || "/favicon.svg";
}

export function createPlacementRoot(placement: RuntimePlacement): HTMLDivElement {
  const root = document.createElement("div");
  root.className = `runtime-placement runtime-placement--${placement.slot}`;
  root.dataset.engineFeature = placement.featureKey;
  root.dataset.enginePlacement = placement.id;
  root.dataset.engineSlot = placement.slot;
  return root;
}

export function configurePlacementRoot(root: HTMLDivElement, placement: RuntimePlacement) {
  root.className = `runtime-placement runtime-placement--${placement.slot}`;
  root.dataset.engineFeature = placement.featureKey;
  root.dataset.enginePlacement = placement.id;
  root.dataset.engineSlot = placement.slot;
  root.removeAttribute("data-runtime-feature-error");
}

export function setReady(root: HTMLDivElement, ready: boolean) {
  if (ready) root.setAttribute("data-runtime-feature-ready", "");
  else root.removeAttribute("data-runtime-feature-ready");
  root.removeAttribute("data-runtime-feature-error");
}

export function showPlacementError(root: HTMLDivElement, message = "Не удалось загрузить раздел") {
  root.removeAttribute("data-runtime-feature-ready");
  root.dataset.runtimeFeatureError = "";
  const alert = document.createElement("p");
  alert.className = "runtime-feature-error";
  alert.setAttribute("role", "alert");
  alert.textContent = message;
  root.replaceChildren(alert);
}

export function findOrCreateMain(root: HTMLElement): HTMLElement {
  const mains = [...root.querySelectorAll("main")];
  if (mains.length > 1) throw new Error("В документе найдено больше одного элемента main.");
  const main = mains[0] ?? document.createElement("main");
  main.classList.add("runtime-slot", "runtime-slot--main");
  if (main.parentElement !== root) root.append(main);
  return main;
}

export function normalizePlacementRoots(root: HTMLElement, composition: Composition, main: HTMLElement): Map<string, HTMLDivElement> {
  const roots = [...root.querySelectorAll<HTMLDivElement>("div.runtime-placement[data-engine-placement]")];
  const byId = new Map<string, HTMLDivElement>();
  for (const placementRoot of roots) {
    const id = placementRoot.dataset.enginePlacement;
    if (id && !byId.has(id)) byId.set(id, placementRoot);
  }
  const rootFor = (placement: RuntimePlacement) => {
    let placementRoot = byId.get(placement.id);
    if (!placementRoot) {
      placementRoot = createPlacementRoot(placement);
      byId.set(placement.id, placementRoot);
    }
    return placementRoot;
  };
  const pageChildren: HTMLElement[] = [];
  const mainRoots: HTMLDivElement[] = [];
  for (const slot of slotOrder) {
    if (slot === "main") {
      const mainPlacements = composition.placements.filter((placement) => placement.slot === "main");
      for (const placement of mainPlacements) {
        const placementRoot = rootFor(placement);
        configurePlacementRoot(placementRoot, placement);
        mainRoots.push(placementRoot);
      }
      main.replaceChildren(...mainRoots);
      pageChildren.push(main);
      continue;
    }
    for (const placement of composition.placements.filter((candidate) => candidate.slot === slot)) {
      const placementRoot = rootFor(placement);
      configurePlacementRoot(placementRoot, placement);
      pageChildren.push(placementRoot);
    }
  }
  root.replaceChildren(...pageChildren);
  return new Map(composition.placements.map((placement) => [placement.id, byId.get(placement.id)!]));
}

export function applyPageIdentity(root: HTMLElement, composition: Composition) {
  for (const className of [...root.classList]) {
    if (className.startsWith("runtime-page--")) root.classList.remove(className);
  }
  root.classList.add("runtime-page", `runtime-page--${composition.page.kind}`);
  root.dataset.snapshot = composition.snapshot.id;
  root.dataset.contentRevision = String(composition.snapshot.contentRevision ?? 0);
}

export function renderState(root: HTMLElement, title: string, detail: string, retry?: () => void) {
  root.replaceChildren();
  root.classList.add("runtime-page");
  delete root.dataset.snapshot;
  delete root.dataset.contentRevision;
  const main = document.createElement("main");
  main.className = "runtime-state";
  const heading = document.createElement("h1");
  heading.textContent = title;
  const paragraph = document.createElement("p");
  paragraph.textContent = detail;
  main.append(heading, paragraph);
  if (retry) {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = "Повторить";
    button.addEventListener("click", retry, { once: true });
    main.append(button);
  }
  root.append(main);
}

export function markDocumentReady(root: HTMLElement, generation: number, currentGeneration: () => number, allReady: boolean, reveal = false) {
  if (generation !== currentGeneration()) return;
  document.documentElement.dataset.engineReady = String(allReady);
  if (allReady || reveal) {
    root.hidden = false;
    document.getElementById("prerender-root")?.remove();
  }
}
