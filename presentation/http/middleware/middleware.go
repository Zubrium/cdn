package middleware

import (
	"context"

	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
	"websiteengine/features/auth"
	"websiteengine/pkg/principal"

	"github.com/google/uuid"

	"websiteengine/pkg/environment"
	"websiteengine/pkg/response"
)

func PrincipalFrom(ctx context.Context) (principal.Principal, bool) {
	return principal.FromContext(ctx)
}

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// RequestID attaches a request ID to the context and response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if !safeRequestID.MatchString(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(response.WithRequestID(r.Context(), id)))
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func Logger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			if sw.status == 0 {
				sw.status = http.StatusOK
			}
			level := slog.LevelInfo
			if sw.status >= 500 {
				level = slog.LevelError
			} else if sw.status >= 400 {
				level = slog.LevelWarn
			}
			log.Log(r.Context(), level, "http_request",
				"request_id", response.RequestID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"bytes", sw.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
			)
		})
	}
}

func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						// A streamed response can already have sent its headers and
						// partial body. Let net/http abort the connection; writing
						// JSON here would corrupt the download while reporting 200.
						panic(rec)
					}
					log.Error("panic recovered",
						"request_id", response.RequestID(r.Context()),
						"panic", rec,
						"stack", string(debug.Stack()),
					)
					response.Error(w, r, http.StatusInternalServerError, "internal_error", "Внутренняя ошибка сервера")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func BodyLimit(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func Authenticate(service *auth.Service, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			secret, ok := response.ParseBearer(r)
			if !ok {
				w.Header().Set("WWW-Authenticate", "Bearer")
				response.Error(w, r, http.StatusUnauthorized, "unauthorized", "Требуется действующий Bearer-токен")
				return
			}
			pr, err := service.Authenticate(r.Context(), secret)
			if err != nil {
				if errors.Is(err, auth.ErrUnauthorized) {
					w.Header().Set("WWW-Authenticate", "Bearer")
					response.Error(w, r, http.StatusUnauthorized, "unauthorized", "Токен отсутствует, истёк или отозван")
					return
				}
				log.Error("authentication backend failed", "request_id", response.RequestID(r.Context()), "error", err)
				response.Error(w, r, http.StatusServiceUnavailable, "auth_unavailable", "Авторизация временно недоступна")
				return
			}
			next.ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), pr)))
		})
	}
}

// WithEnvironment tags a public listener with its environment. A listener is
// the trust boundary: clients cannot choose dev or prod through headers.
func WithEnvironment(env environment.Environment) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(environment.WithContext(r.Context(), env)))
		})
	}
}

func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			response.Error(w, r, http.StatusUnauthorized, "unauthorized", "Требуется авторизация")
			return
		}
		if !auth.ValidRole(p.Role) {
			response.Error(w, r, http.StatusForbidden, "forbidden", "Недостаточно прав")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireStaff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			response.Error(w, r, http.StatusUnauthorized, "unauthorized", "Требуется авторизация")
			return
		}
		if !auth.IsStaff(p.Role) {
			response.Error(w, r, http.StatusForbidden, "forbidden", "Недостаточно прав")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireSiteManager(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			response.Error(w, r, http.StatusUnauthorized, "unauthorized", "Требуется авторизация")
			return
		}
		if !auth.IsSiteManager(p.Role) {
			response.Error(w, r, http.StatusForbidden, "forbidden", "Недостаточно прав")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			response.Error(w, r, http.StatusUnauthorized, "unauthorized", "Требуется авторизация")
			return
		}
		if !auth.IsAdmin(p.Role) {
			response.Error(w, r, http.StatusForbidden, "forbidden", "Недостаточно прав")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP returns the peer address. Forwarded headers are trusted only when
// the server is explicitly configured behind a trusted reverse proxy.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(realIP) != nil {
			return realIP
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// CORS exposes only explicitly configured public origins. It is applied to
// public content and form-submission routes, never to admin routes.
func CORS(origins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return corsHandler(origins, next)
	}
}

func corsHandler(origins []string, next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(origins))
	allowAny := false
	for _, origin := range origins {
		if origin == "*" {
			allowAny = true
			continue
		}
		allowed[origin] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")
		if !allowAny && !allowed[origin] {
			if r.Method == http.MethodOptions {
				response.Error(w, r, http.StatusForbidden, "cors_origin_denied", "Origin не разрешён")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if allowAny {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, X-RateLimit-Limit, X-RateLimit-Remaining")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CORSFor selects the allow-list for the configured branch attached to the request.
func CORSFor(origins map[environment.Environment][]string) func(http.Handler) http.Handler {
	handlers := make(map[environment.Environment]func(http.Handler) http.Handler, len(origins))
	for env, list := range origins {
		handlers[env] = CORS(list)
	}
	fallback := CORS(nil)
	return func(next http.Handler) http.Handler {
		wrapped := make(map[environment.Environment]http.Handler, len(handlers))
		for env, wrap := range handlers {
			wrapped[env] = wrap(next)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			env := environment.FromContext(r.Context())
			if h, ok := wrapped[env]; ok {
				h.ServeHTTP(w, r)
				return
			}
			fallback(next).ServeHTTP(w, r)
		})
	}
}
