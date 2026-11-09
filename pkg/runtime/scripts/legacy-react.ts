import * as React from "react";
import { createRoot, hydrateRoot } from "react-dom/client";
import type { LegacyFeatureInstance, LegacyFeatureProps } from "./runtime-types";

type LegacyFeatureModule = { default: React.ComponentType<LegacyFeatureProps> };

/**
 * Mounts the existing legacy React feature contract on demand. React is only
 * fetched with this module when a placement actually uses legacy-react-v1;
 * current static module and HTML placements do not load this adapter.
 */
export async function mountLegacyReact(
  host: HTMLElement,
  entryURL: string,
  initialProps: LegacyFeatureProps,
  onError: (error: unknown) => void,
): Promise<LegacyFeatureInstance> {
  const featurePromise = import(/* @vite-ignore */ entryURL) as Promise<LegacyFeatureModule>;
  const [featureModule] = await Promise.all([featurePromise]);
  if (typeof featureModule.default !== "function") {
    throw new Error("Legacy React feature must export a default component");
  }

  const Feature = featureModule.default;
  const hasPrerenderedMarkup = host.childNodes.length > 0;
  if (hasPrerenderedMarkup) {
    host.querySelectorAll("form[inert]").forEach((form) => form.removeAttribute("inert"));
  }
  let root: ReturnType<typeof createRoot> | undefined;
  let disposed = false;
  let pending: { resolve: () => void; reject: (error: unknown) => void } | undefined;

  class FeatureErrorBoundary extends React.Component<
    { children: React.ReactNode },
    { failed: boolean }
  > {
    state = { failed: false };

    static getDerivedStateFromError() {
      return { failed: true };
    }

    componentDidCatch(error: Error) {
      onError(error);
      pending?.reject(error);
      pending = undefined;
    }

    render() {
      return this.state.failed ? null : this.props.children;
    }
  }

  function CommitSignal({ onCommit }: { onCommit: () => void }) {
    React.useLayoutEffect(onCommit, [onCommit]);
    return null;
  }

  function render(props: LegacyFeatureProps, hydrateInitial = false): Promise<void> {
    if (disposed) return Promise.resolve();
    return new Promise<void>((resolve, reject) => {
      pending?.resolve();
      pending = { resolve, reject };
      const commit = () => {
        if (pending?.resolve !== resolve) return;
        pending = undefined;
        resolve();
      };
      const tree = React.createElement(
          FeatureErrorBoundary,
          null,
          React.createElement(
            React.Fragment,
            null,
            React.createElement(Feature, props),
            React.createElement(CommitSignal, { onCommit: commit }),
          ),
        );
      if (hydrateInitial && !root) {
        root = hydrateRoot(host, tree, {
          onRecoverableError() {
            console.error("Legacy feature hydration recovered from a rendering mismatch");
          },
        });
      } else {
        root ??= createRoot(host);
        root.render(tree);
      }
    });
  }

  const ready = render(initialProps, hasPrerenderedMarkup);
  return {
    ready,
    update: render,
    unmount() {
      if (disposed) return;
      disposed = true;
      pending?.resolve();
      pending = undefined;
      root?.unmount();
    },
  };
}
