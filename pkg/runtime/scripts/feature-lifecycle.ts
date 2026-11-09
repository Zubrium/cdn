import { parseStaticHTMLFragment } from "./static-html";
import { ensureStyles, loadFeatureModule } from "./feature-assets";
import { featureSDK } from "./feature-sdk";
import { setReady, showPlacementError } from "./document-dom";
import type {
  Composition,
  FeatureContext,
  FeatureInstance,
  FeatureRouteContext,
  LegacyFeatureInstance,
  LegacyFeatureProps,
  MountedFeature,
  ModuleFeature,
  RuntimePlacement,
} from "./runtime-types";

export function routeFor(path: string, search: string, composition: Composition): FeatureRouteContext {
  return { path, params: composition.routeArgs ?? {}, search };
}

export function featureContext(placement: RuntimePlacement, systemAssets: Composition["systemAssets"], route: FeatureRouteContext, signal: AbortSignal): FeatureContext {
  return {
    placementId: placement.id,
    featureKey: placement.featureKey,
    version: placement.version,
    arguments: placement.arguments,
    content: placement.content ?? {},
    detailLink: placement.detailLink,
    route,
    systemAssets: systemAssets ?? {},
    sdk: featureSDK,
    signal,
  };
}

export function legacyProps(placement: RuntimePlacement, systemAssets?: Composition["systemAssets"]): LegacyFeatureProps {
  return {
    content: placement.content ?? {},
    arguments: placement.arguments,
    featureKey: placement.featureKey,
    detailLink: placement.detailLink,
    systemAssets,
    sdk: featureSDK,
  };
}

export function identityFor(placement: RuntimePlacement): string {
  return JSON.stringify([placement.runtimeAbi ?? "legacy-react-v1", placement.entryUrl, placement.styleUrls ?? [], placement.version, placement.featureKey]);
}

export function contextKeyFor(placement: RuntimePlacement, route: FeatureRouteContext, systemAssets?: Composition["systemAssets"]): string {
  return JSON.stringify([placement.arguments, placement.content, placement.detailLink, route, systemAssets]);
}

export async function mountPlacement(
  placement: RuntimePlacement,
  root: HTMLDivElement,
  hasPrerenderedMarkup: boolean,
  controller: AbortController,
  route: FeatureRouteContext,
  systemAssets: Composition["systemAssets"],
  onError: (error: unknown) => void,
): Promise<MountedFeature> {
  const mounted: MountedFeature = {
    abi: placement.runtimeAbi ?? "legacy-react-v1",
    identity: identityFor(placement),
    root,
    controller,
    contextKey: contextKeyFor(placement, route, systemAssets),
    ready: false,
    failed: false,
  };
  const fail = (error: unknown) => {
    if (controller.signal.aborted) return;
    mounted.failed = true;
    onError(error);
  };
  try {
    await ensureStyles(placement.styleUrls ?? []);
    if (controller.signal.aborted) return mounted;
    const abi = placement.runtimeAbi ?? "legacy-react-v1";
    if (abi === "static-html-v1") {
      if (hasPrerenderedMarkup && root.childNodes.length > 0) {
        mounted.ready = true;
        setReady(root, true);
        return mounted;
      }
      const response = await fetch(placement.entryUrl, { headers: { Accept: "text/html" }, signal: controller.signal });
      if (!response.ok) throw new Error(`HTML feature request failed (${response.status})`);
      root.replaceChildren(parseStaticHTMLFragment(await response.text(), placement.entryUrl));
      mounted.ready = true;
      setReady(root, true);
      return mounted;
    }
    if (abi === "static-module-v1") {
      const module = await loadFeatureModule(placement.entryUrl);
      if (!("mount" in module) || typeof module.mount !== "function") throw new Error("Module must export mount(root, context)");
      const instance = await (module as ModuleFeature).mount(root, featureContext(placement, systemAssets, route, controller.signal));
      if (!instance || typeof instance.update !== "function" || typeof instance.unmount !== "function" || !instance.ready || typeof instance.ready.then !== "function") {
        throw new Error("mount must return ready, update, and unmount");
      }
      mounted.instance = instance;
      if (controller.signal.aborted) {
        await instance.unmount();
        return mounted;
      }
      await instance.ready;
      if (controller.signal.aborted) return mounted;
      mounted.ready = true;
      setReady(root, true);
      return mounted;
    }
    if (abi === "legacy-react-v1") {
      const { mountLegacyReact } = await import("./legacy-react");
      const instance = await mountLegacyReact(root, placement.entryUrl, legacyProps(placement, systemAssets), fail);
      mounted.instance = instance;
      if (controller.signal.aborted) {
        instance.unmount();
        return mounted;
      }
      await instance.ready;
      if (!mounted.failed && !controller.signal.aborted) {
        mounted.ready = true;
        setReady(root, true);
      }
      return mounted;
    }
    throw new Error("Неизвестный формат фичи");
  } catch (error) {
    fail(error);
    if (!controller.signal.aborted) showPlacementError(root);
    return mounted;
  }
}

export async function updatePlacement(
  mounted: MountedFeature,
  placement: RuntimePlacement,
  route: FeatureRouteContext,
  systemAssets: Composition["systemAssets"],
  onError: (error: unknown) => void,
): Promise<void> {
  const nextKey = contextKeyFor(placement, route, systemAssets);
  if (nextKey === mounted.contextKey || !mounted.instance) return;
  mounted.controller.abort();
  const controller = new AbortController();
  mounted.controller = controller;
  mounted.contextKey = nextKey;
  mounted.ready = false;
  mounted.failed = false;
  setReady(mounted.root, false);
  try {
    const instance = mounted.instance;
    if (mounted.abi === "static-module-v1") {
      await (instance as FeatureInstance).update(featureContext(placement, systemAssets, route, controller.signal));
    } else if (mounted.abi === "legacy-react-v1") {
      await (instance as LegacyFeatureInstance).update(legacyProps(placement, systemAssets));
    }
    if (controller.signal.aborted) return;
    mounted.ready = true;
    setReady(mounted.root, true);
  } catch (error) {
    if (controller.signal.aborted) return;
    mounted.failed = true;
    showPlacementError(mounted.root);
    onError(error);
  }
}
