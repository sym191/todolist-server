package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"

	"github.com/sym191/todolist-server/internal/auth"
	"github.com/sym191/todolist-server/internal/config"
	"github.com/sym191/todolist-server/internal/store"
	"github.com/sym191/todolist-server/internal/syncmodel"
)

type Backend interface {
	Ping(context.Context) error
	Register(context.Context, string, string, string, string, []byte, time.Time) (store.User, string, error)
	UserByEmail(context.Context, string) (store.User, error)
	UserByID(context.Context, string) (store.User, error)
	CreateSession(context.Context, string, string, string, []byte, time.Time) (string, error)
	RotateRefreshToken(context.Context, []byte, []byte, time.Time) (store.Session, error)
	RevokeRefreshToken(context.Context, []byte) error
	ApplyMutations(context.Context, string, []syncmodel.Mutation) ([]syncmodel.MutationResult, error)
	PullChanges(context.Context, string, int64, int) ([]syncmodel.Change, int64, bool, error)
	Snapshot(context.Context, string) (syncmodel.Snapshot, error)
}

type API struct {
	config config.Config
	store  Backend
	auth   *auth.Service
	logger *slog.Logger
}

func New(cfg config.Config, backend Backend, authService *auth.Service, logger *slog.Logger) http.Handler {
	api := &API{config: cfg, store: backend, auth: authService, logger: logger}
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(api.securityHeaders)
	router.Use(api.cors)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(30 * time.Second))
	router.Use(api.accessLog)

	router.Get("/health/live", api.live)
	router.Get("/health/ready", api.ready)
	router.Route("/api/v1", func(router chi.Router) {
		router.Route("/auth", func(router chi.Router) {
			router.Use(httprate.LimitByIP(10, time.Minute))
			router.Post("/register", api.register)
			router.Post("/login", api.login)
			router.Post("/refresh", api.refresh)
			router.Post("/logout", api.logout)
		})
		router.Group(func(router chi.Router) {
			router.Use(api.authenticate)
			router.Get("/me", api.me)
			router.Get("/sync/snapshot", api.snapshot)
			router.Get("/sync/pull", api.pull)
			router.Post("/sync/push", api.push)
		})
	})
	return router
}

func (api *API) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := api.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

type credentialsRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type authResponse struct {
	User         publicUser `json:"user"`
	DeviceID     string     `json:"deviceId"`
	AccessToken  string     `json:"accessToken"`
	RefreshToken string     `json:"refreshToken"`
	ExpiresAt    time.Time  `json:"expiresAt"`
}

type publicUser struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"createdAt"`
}

func (api *API) register(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if !api.decode(w, r, &request) {
		return
	}
	email, err := normalizedEmail(request.Email)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_email", err.Error())
		return
	}
	passwordHash, err := auth.HashPassword(request.Password)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_password", err.Error())
		return
	}
	plainRefresh, refreshHash, refreshExpiry, err := api.auth.NewRefreshToken()
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	user, deviceID, err := api.store.Register(
		r.Context(), email, passwordHash, request.DeviceID, request.DeviceName,
		refreshHash, refreshExpiry,
	)
	if errors.Is(err, store.ErrEmailExists) {
		writeError(w, http.StatusConflict, "email_exists", "an account with this email already exists")
		return
	}
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	response, err := api.authResponse(user, deviceID, plainRefresh)
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

func (api *API) login(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if !api.decode(w, r, &request) {
		return
	}
	email, err := normalizedEmail(request.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
		return
	}
	user, err := api.store.UserByEmail(r.Context(), email)
	if err != nil || !auth.VerifyPassword(user.PasswordHash, request.Password) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
		return
	}
	plainRefresh, refreshHash, refreshExpiry, err := api.auth.NewRefreshToken()
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	deviceID, err := api.store.CreateSession(
		r.Context(), user.ID, request.DeviceID, request.DeviceName, refreshHash, refreshExpiry,
	)
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	response, err := api.authResponse(user, deviceID, plainRefresh)
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) refresh(w http.ResponseWriter, r *http.Request) {
	var request refreshRequest
	if !api.decode(w, r, &request) {
		return
	}
	if request.RefreshToken == "" {
		writeError(w, http.StatusUnprocessableEntity, "refresh_token_required", "refreshToken is required")
		return
	}
	plainRefresh, newHash, newExpiry, err := api.auth.NewRefreshToken()
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	session, err := api.store.RotateRefreshToken(
		r.Context(), auth.HashRefreshToken(request.RefreshToken), newHash, newExpiry,
	)
	if errors.Is(err, store.ErrInvalidRefresh) {
		writeError(w, http.StatusUnauthorized, "invalid_refresh_token", "refresh token is invalid or expired")
		return
	}
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	user, err := api.store.UserByID(r.Context(), session.UserID)
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	response, err := api.authResponse(user, session.DeviceID, plainRefresh)
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) logout(w http.ResponseWriter, r *http.Request) {
	var request refreshRequest
	if !api.decode(w, r, &request) {
		return
	}
	if request.RefreshToken != "" {
		if err := api.store.RevokeRefreshToken(r.Context(), auth.HashRefreshToken(request.RefreshToken)); err != nil {
			api.internalError(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) me(w http.ResponseWriter, r *http.Request) {
	user, err := api.store.UserByID(r.Context(), identityFromContext(r.Context()).UserID)
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPublicUser(user))
}

type pushRequest struct {
	Mutations []syncmodel.Mutation `json:"mutations"`
}

func (api *API) push(w http.ResponseWriter, r *http.Request) {
	var request pushRequest
	if !api.decode(w, r, &request) {
		return
	}
	if len(request.Mutations) == 0 || len(request.Mutations) > api.config.SyncBatchSize {
		writeError(w, http.StatusUnprocessableEntity, "invalid_batch_size",
			fmt.Sprintf("mutations must contain between 1 and %d items", api.config.SyncBatchSize))
		return
	}
	results, err := api.store.ApplyMutations(
		r.Context(), identityFromContext(r.Context()).UserID, request.Mutations,
	)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "mutation_rejected", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (api *API) pull(w http.ResponseWriter, r *http.Request) {
	cursor, err := queryInt64(r, "cursor", 0)
	if err != nil || cursor < 0 {
		writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a non-negative integer")
		return
	}
	limit, err := queryInt(r, "limit", api.config.SyncBatchSize)
	if err != nil || limit < 1 || limit > api.config.SyncBatchSize {
		writeError(w, http.StatusBadRequest, "invalid_limit",
			fmt.Sprintf("limit must be between 1 and %d", api.config.SyncBatchSize))
		return
	}
	changes, newCursor, hasMore, err := api.store.PullChanges(
		r.Context(), identityFromContext(r.Context()).UserID, cursor, limit,
	)
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"changes": changes, "cursor": newCursor, "hasMore": hasMore,
	})
}

func (api *API) snapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := api.store.Snapshot(r.Context(), identityFromContext(r.Context()).UserID)
	if err != nil {
		api.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (api *API) authResponse(user store.User, deviceID, refreshToken string) (authResponse, error) {
	accessToken, expiresAt, err := api.auth.NewAccessToken(user.ID, deviceID)
	if err != nil {
		return authResponse{}, err
	}
	return authResponse{
		User: toPublicUser(user), DeviceID: deviceID, AccessToken: accessToken,
		RefreshToken: refreshToken, ExpiresAt: expiresAt,
	}, nil
}

func toPublicUser(user store.User) publicUser {
	return publicUser{ID: user.ID, Email: user.Email, CreatedAt: user.CreatedAt}
}

func (api *API) decode(w http.ResponseWriter, r *http.Request, destination any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, api.config.MaxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON value")
		return false
	}
	return true
}

func normalizedEmail(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || len(value) > 254 {
		return "", errors.New("email must be a valid address")
	}
	return value, nil
}

func queryInt64(r *http.Request, name string, fallback int64) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func queryInt(r *http.Request, name string, fallback int) (int, error) {
	value, err := queryInt64(r, name, int64(fallback))
	return int(value), err
}

func (api *API) internalError(w http.ResponseWriter, r *http.Request, err error) {
	api.logger.ErrorContext(r.Context(), "request failed",
		"error", err, "request_id", middleware.GetReqID(r.Context()))
	writeError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
