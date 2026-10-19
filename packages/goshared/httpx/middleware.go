package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/idgen"
	"github.com/karmarat/platform/packages/goshared/logging"
)

type requestIDKey struct{}

// RequestIDHeader is echoed on every response so a client can quote it in a report.
const RequestIDHeader = "X-Request-ID"

// RequestID attaches a request identifier, reusing an upstream one when present
// so a single identifier follows a request across every service hop.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = idgen.New("req")
		}

		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request identifier.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// statusRecorder captures the status code for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush forwards to the underlying writer so streaming responses still work.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Logger records one structured line per request and puts a request-scoped
// logger on the context.
func Logger(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w}

			requestLogger := base.With(
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)
			ctx := logging.WithContext(r.Context(), requestLogger)

			next.ServeHTTP(recorder, r.WithContext(ctx))

			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}

			level := slog.LevelInfo
			switch {
			case status >= 500:
				level = slog.LevelError
			case status >= 400:
				level = slog.LevelWarn
			}

			requestLogger.Log(r.Context(), level, "request",
				slog.Int("status", status),
				slog.Int("bytes", recorder.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("host", r.Host),
			)
		})
	}
}

// Recoverer turns a panic into a 500 instead of dropping the connection.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				// http.ErrAbortHandler is a deliberate abort, not a bug.
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}

				logging.FromContext(r.Context()).Error("panic recovered",
					slog.Any("panic", recovered),
					slog.String("stack", string(debug.Stack())),
				)
				WriteProblem(w, r, Internal("An unexpected error occurred."))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders sets the response headers every portal should carry.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// CORSConfig controls cross-origin access.
type CORSConfig struct {
	// PlatformHostname enables any subdomain of the platform, which is what the
	// portals need: root, jobs, and every company slug are separate origins.
	PlatformHostname string
	// ExtraOrigins are additional exact origins to allow.
	ExtraOrigins   []string
	AllowedMethods []string
	AllowedHeaders []string
	MaxAge         time.Duration
}

// CORS allows the platform's own portals and nothing else.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	if len(cfg.AllowedMethods) == 0 {
		cfg.AllowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	}
	if len(cfg.AllowedHeaders) == 0 {
		cfg.AllowedHeaders = []string{"Authorization", "Content-Type", RequestIDHeader, "X-Company-Slug"}
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 10 * time.Minute
	}

	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	maxAge := int(cfg.MaxAge.Seconds())

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && cfg.allows(origin) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Add("Vary", "Origin")

				if r.Method == http.MethodOptions {
					h.Set("Access-Control-Allow-Methods", methods)
					h.Set("Access-Control-Allow-Headers", headers)
					h.Set("Access-Control-Max-Age", itoa(maxAge))
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (c CORSConfig) allows(origin string) bool {
	for _, allowed := range c.ExtraOrigins {
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	if c.PlatformHostname == "" {
		return false
	}

	host := origin
	for _, scheme := range []string{"https://", "http://"} {
		host = strings.TrimPrefix(host, scheme)
	}
	if idx := strings.Index(host, "/"); idx != -1 {
		host = host[:idx]
	}
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)

	return host == c.PlatformHostname || strings.HasSuffix(host, "."+c.PlatformHostname)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Timeout aborts a request that outlives the budget.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
