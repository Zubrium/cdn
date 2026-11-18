import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  UNAUTHORIZED_EVENT,
  api,
  isAbortError,
} from "@/api";
import type { Principal } from "@/types";
import { errorMessage } from "@/lib/errors";
import { classifyAuthProbeFailure } from "./auth-state";

export type AuthStatus =
  | "checking"
  | "anonymous"
  | "authenticated"
  | "unavailable";

interface AuthContextValue {
  principal: Principal | null;
  status: AuthStatus;
  error: string | null;
  notice: string | null;
  login: (credential: string) => Promise<Principal>;
  logout: () => void;
  retry: () => Promise<void>;
  clearNotice: () => void;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [principal, setPrincipal] = useState<Principal | null>(null);
  const [status, setStatus] = useState<AuthStatus>("checking");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const clearState = useCallback((message?: string) => {
    void queryClient.cancelQueries();
    queryClient.clear();
    setPrincipal(null);
    setStatus("anonymous");
    setError(null);
    setNotice(message ?? null);
  }, [queryClient]);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
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
        const failure = classifyAuthProbeFailure(meError);
        if (failure.status === "anonymous") {
          clearState();
          return;
        }
        setError(failure.message);
        setStatus(failure.status);
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

  const login = useCallback(async (credential: string) => {
    const value = credential.trim();
    void queryClient.cancelQueries();
    queryClient.clear();
    setNotice(null);
    setError(null);
    try {
      const me = await api.auth.login(value);
      setPrincipal(me);
      setStatus("authenticated");
      return me;
    } catch (loginError) {
      if (loginError instanceof ApiError && loginError.status === 401) {
        setPrincipal(null);
        setStatus("anonymous");
        throw loginError;
      }
      setError(errorMessage(loginError));
      setStatus("unavailable");
      throw loginError;
    }
  }, [queryClient]);

  const retry = useCallback(async () => {
    setStatus("checking");
    setError(null);
    try {
      const me = await api.auth.me();
      setPrincipal(me);
      setStatus("authenticated");
    } catch (retryError) {
      const failure = classifyAuthProbeFailure(retryError);
      if (failure.status === "anonymous") {
        clearState();
        return;
      }
      setError(failure.message);
      setStatus(failure.status);
    }
  }, [clearState]);

  const logout = useCallback(() => {
    void api.auth.logout().catch(() => undefined);
    clearState();
  }, [clearState]);
  const clearNotice = useCallback(() => setNotice(null), []);

  const value = useMemo<AuthContextValue>(
    () => ({
      principal,
      status,
      error,
      notice,
      login,
      logout,
      retry,
      clearNotice,
    }),
    [principal, status, error, notice, login, logout, retry, clearNotice],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useAuth must be used inside AuthProvider");
  return context;
}
