import { useCallback, useRef, useState } from "react";

interface HistoryState<T> {
  past: T[];
  present: T;
  future: T[];
}

const LIMIT = 60;
const COALESCE_MS = 700;

/**
 * Document editor history. Typing changes can be coalesced into one undo step;
 * structural operations create a separate step. The present value is the single
 * source of truth for the editor.
 */
export function useEditorHistory<T>(initial: T) {
  const [state, setState] = useState<HistoryState<T>>({
    past: [],
    present: initial,
    future: [],
  });
  const lastUpdate = useRef(0);

  const update = useCallback(
    (updater: (current: T) => T, coalesce = false) => {
      setState((current) => {
        const next = updater(current.present);
        if (JSON.stringify(next) === JSON.stringify(current.present)) {
          return current;
        }
        const now = Date.now();
        const shouldPush =
          !coalesce || now - lastUpdate.current > COALESCE_MS;
        lastUpdate.current = now;
        return {
          past: shouldPush
            ? [...current.past, current.present].slice(-LIMIT)
            : current.past,
          present: next,
          future: [],
        };
      });
    },
    [],
  );

  const replace = useCallback((value: T) => {
    lastUpdate.current = 0;
    setState({ past: [], present: value, future: [] });
  }, []);

  const undo = useCallback(() => {
    setState((current) => {
      if (current.past.length === 0) return current;
      const previous = current.past[current.past.length - 1] as T;
      return {
        past: current.past.slice(0, -1),
        present: previous,
        future: [current.present, ...current.future].slice(0, LIMIT),
      };
    });
    lastUpdate.current = 0;
  }, []);

  const redo = useCallback(() => {
    setState((current) => {
      if (current.future.length === 0) return current;
      const next = current.future[0] as T;
      return {
        past: [...current.past, current.present].slice(-LIMIT),
        present: next,
        future: current.future.slice(1),
      };
    });
    lastUpdate.current = 0;
  }, []);

  return {
    value: state.present,
    update,
    replace,
    undo,
    redo,
    canUndo: state.past.length > 0,
    canRedo: state.future.length > 0,
  };
}
