// static-lifecycle browser host.
// The host owns one lifecycle operation per placement, cooperative cancellation,
// bounded waiting and error isolation. Feature code only receives the public
// context and an AbortSignal; it never sees host services or credentials.

const LIFECYCLE = Object.freeze(['render', 'hydrate', 'update', 'dispose']);

function assertExports(module) {
  for (const name of LIFECYCLE) {
    if (typeof module[name] !== 'function') {
      throw new Error('missing lifecycle export: ' + name);
    }
  }
}

function serializableContext(context) {
  if (!context || typeof context !== 'object' || typeof context.placementId !== 'string') {
    throw new Error('invalid feature context');
  }
  const forbidden = ['credential', 'password', 'secret', 'token', 'apikey', 'authorization', 'cookie'];
  for (const key of Object.keys(context)) {
    if (forbidden.some((part) => key.toLowerCase().includes(part)) && key !== 'signal') {
      throw new Error('secret-like key in feature context');
    }
  }
  return Object.freeze({ ...context });
}

function timeoutPromise(promise, timeout, signal) {
  if (!Number.isFinite(timeout) || timeout <= 0) {
    return promise;
  }
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('lifecycle timeout')), timeout);
    promise.then(
      (value) => { clearTimeout(timer); resolve(value); },
      (error) => { clearTimeout(timer); reject(error); },
    );
    if (signal) {
      signal.addEventListener('abort', () => { clearTimeout(timer); }, { once: true });
    }
  });
}

export function createFeatureHost(importer, options = {}) {
  if (typeof importer !== 'function') {
    throw new Error('feature module importer is required');
  }
  const timeout = Number(options.timeout) || 0;
  const placements = new Map();

  function placementFor(placementId, entry) {
    let placement = placements.get(placementId);
    if (!placement) {
      placement = { module: null, entry, root: null, active: null, pending: null, controller: null, disposed: false };
      placements.set(placementId, placement);
    }
    return placement;
  }

  async function load(placement) {
    if (!placement.module) {
      const module = await importer(placement.entry);
      assertExports(module);
      placement.module = module;
    }
    return placement.module;
  }

  async function invoke(placement, operation, context) {
    if (placement.disposed) {
      return { ok: false, code: 'disposed' };
    }
    if (placement.active) {
      if (operation !== 'update') {
        return { ok: false, code: 'busy' };
      }
      placement.pending = context;
      placement.controller.abort();
      return { ok: true, code: 'queued' };
    }
    const controller = new AbortController();
    placement.controller = controller;
    const prepared = { ...serializableContext(context), signal: controller.signal };
    placement.active = operation;
    try {
      const module = await load(placement);
      const call = module[operation](placement.root, prepared);
      await timeoutPromise(Promise.resolve(call), timeout, controller.signal);
      return { ok: true };
    } catch (error) {
      return { ok: false, code: error && error.message ? 'lifecycle_failed' : 'unknown' };
    } finally {
      placement.active = null;
      placement.controller = null;
      const next = placement.pending;
      placement.pending = null;
      if (next && !placement.disposed) {
        await invoke(placement, 'update', next);
      }
    }
  }

  return Object.freeze({
    render: (placementId, entry, root, context) => {
      const placement = placementFor(placementId, entry);
      placement.root = root;
      return invoke(placement, 'render', context);
    },
    hydrate: (placementId, entry, root, context) => {
      const placement = placementFor(placementId, entry);
      placement.root = root;
      return invoke(placement, 'hydrate', context);
    },
    update: (placementId, entry, context) => invoke(placementFor(placementId, entry), 'update', context),
    dispose: async (placementId, entry) => {
      const placement = placements.get(placementId);
      if (!placement || placement.disposed) {
        return { ok: true };
      }
      if (placement.controller) {
        placement.controller.abort();
      }
      placement.disposed = true;
      try {
        if (placement.module) {
          await timeoutPromise(Promise.resolve(placement.module.dispose(placement.root)), timeout);
        }
      } catch (error) {
        return { ok: false, code: 'dispose_failed' };
      } finally {
        placements.delete(placementId);
      }
      return { ok: true };
    },
  });
}
