package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

type identity struct {
	UserID   string
	DeviceID string
}

type identityKey struct{}

func (api *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeError(w, http.StatusUnauthorized, "authentication_required", "a bearer access token is required")
			return
		}
		claims, err := api.auth.ParseAccessToken(strings.TrimSpace(parts[1]))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_access_token", "access token is invalid or expired")
			return
		}
		ctx := context.WithValue(r.Context(), identityKey{}, identity{
			UserID: claims.Subject, DeviceID: claims.DeviceID,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func identityFromContext(ctx context.Context) identity {
	value, _ := ctx.Value(identityKey{}).(identity)
	return value
}

func (api *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (api *API) cors(next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(api.config.AllowedOrigins))
	for _, origin := range api.config.AllowedOrigins {
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (api *API) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)
		api.logger.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", wrapped.Status()),
			slog.Duration("duration", time.Since(started)),
			slog.String("request_id", middleware.GetReqID(r.Context())),
		)
	})
}
