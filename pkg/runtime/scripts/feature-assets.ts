import type { ModuleFeature } from "./runtime-types";

const moduleCache = new Map<string, Promise<ModuleFeature | Record<string, unknown>>>();

const styleRequests = new Map<string, Promise<void>>();

export function loadFeatureModule(url: string): Promise<ModuleFeature | Record<string, unknown>> {
  const cached = moduleCache.get(url);
  if (cached) return cached;
  const request = import(/* @vite-ignore */ url) as Promise<ModuleFeature | Record<string, unknown>>;
  moduleCache.set(url, request);
  void request.catch(() => moduleCache.delete(url));
  return request;
}

export function ensureStyles(urls: string[]): Promise<void> {
  return Promise.all([...new Set(urls)].map((href) => {
    const existingRequest = styleRequests.get(href);
    if (existingRequest) return existingRequest;
    const existingLink = [...document.querySelectorAll<HTMLElement>("link[data-feature-style], style[data-feature-style]")]
      .find((candidate) => candidate.dataset.featureStyle === href);
    if (existingLink?.dataset.loaded === "true") return Promise.resolve();
    const stylesheet = document.createElement("link");
    stylesheet.rel = "stylesheet";
    stylesheet.href = href;
    stylesheet.dataset.featureStyle = href;
    const request = new Promise<void>((resolve, reject) => {
      stylesheet.onload = () => {
        stylesheet.dataset.loaded = "true";
        resolve();
      };
      stylesheet.onerror = () => {
        stylesheet.remove();
        styleRequests.delete(href);
        reject(new Error(`Не удалось загрузить стили сайта: ${href}`));
      };
    });
    styleRequests.set(href, request);
    if (existingLink?.isConnected) existingLink.replaceWith(stylesheet);
    else document.head.append(stylesheet);
    return request;
  })).then(() => undefined);
}
