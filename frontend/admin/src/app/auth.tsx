import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  ApiError,
  UNAUTHORIZED_EVENT,
  api,
  getStoredToken,
  isAbortError,
  setStoredToken,
} from "@/api";
import type { Principal } from "@/types";
import { errorMessage } from "@/lib/errors";

export type AuthStatus =
  | "checking"
  | "anonymous"
  | "authenticated"
  | "unavailable";

interface AuthContextValue {
  token: string | null;
  principal: Principal | null;
  status: AuthStatus;
  error: string | null;
  notice: string | null;
  login: (token: string) => Promise<Principal>;
  logout: () => void;
  retry: () => Promise<void>;
  clearNotice: () => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const initial = getStoredToken();
  const [token, setToken] = useState<string | null>(initial);
  const [principal, setPrincipal] = useState<Principal | null>(null);
  const [status, setStatus] = useState<AuthStatus>(
    initial ? "checking" : "anonymous",
  );
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const clearState = useCallback((message?: string) => {
    setStoredToken(null);
    setToken(null);
    setPrincipal(null);
    setStatus("anonymous");
    setError(null);
    setNotice(message ?? null);
  }, []);

  useEffect(() => {
    const stored = getStoredToken();
    if (!stored) {
      setStatus("anonymous");
      return;
    }
    let cancelled = false;
    const controller = new AbortController();
    setToken(stored);
    setStatus("checking");
    api.auth
      .me(controller.signal)
      .then((me) => {
        if (cancelled) return;
        setPrincipal(me);
        setStatus("authenticated");
        setError(null);
      })
      .catch((meError: unknown) => {
        if (cancelled || isAbortError(meError)) return;
        if (meError instanceof ApiError && meError.status === 401) {
          clearState("Сессия истекла. Войдите снова.");
          return;
        }
        setError(errorMessage(meError));
        setStatus("unavailable");
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [clearState]);

  useEffect(() => {
    const onUnauthorized = (event: Event) => {
      const detail = (event as CustomEvent<string>).detail;
      clearState(detail || "Сессия истекла. Войдите снова.");
    };
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }, [clearState]);

  const login = useCallback(async (secret: string) => {
    const value = secret.trim();
    setNotice(null);
    setError(null);
    setStoredToken(value);
    setToken(value);
    try {
      const me = await api.auth.me();
      setPrincipal(me);
      setStatus("authenticated");
      return me;
    } catch (loginError) {
      if (loginError instanceof ApiError && loginError.status === 401) {
        setStoredToken(null);
        setToken(null);
        setPrincipal(null);
        setStatus("anonymous");
        throw loginError;
      }
      setError(errorMessage(loginError));
      setStatus("unavailable");
      throw loginError;
    }
  }, []);

  const retry = useCallback(async () => {
    const stored = getStoredToken();
    if (!stored) {
      clearState();
      return;
    }
    setStatus("checking");
    setError(null);
    try {
      const me = await api.auth.me();
      setToken(stored);
      setPrincipal(me);
      setStatus("authenticated");
    } catch (retryError) {
      if (retryError instanceof ApiError && retryError.status === 401) {
        clearState("Сессия истекла. Войдите снова.");
        return;
      }
      setError(errorMessage(retryError));
      setStatus("unavailable");
    }
  }, [clearState]);

  const logout = useCallback(() => clearState(), [clearState]);
  const clearNotice = useCallback(() => setNotice(null), []);

  const value = useMemo<AuthContextValue>(
    () => ({
      token,
      principal,
      status,
      error,
      notice,
      login,
      logout,
      retry,
      clearNotice,
    }),
    [token, principal, status, error, notice, login, logout, retry, clearNotice],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useAuth must be used inside AuthProvider");
  return context;
}
