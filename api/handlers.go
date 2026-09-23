package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	maxBodyBytes     = 1 << 20
	maxTitleRunes    = 200
	maxCaseDeadlines = 20
)

type store interface {
	ListCases(ctx context.Context) ([]Case, error)
	CreateCase(ctx context.Context, title string, deadlines []NewDeadline) (Case, []Deadline, error)
	DeleteCase(ctx context.Context, id int) error
	CreateDeadline(ctx context.Context, caseID int, d NewDeadline) (Deadline, error)
	ListDeadlines(ctx context.Context) ([]Deadline, error)
	UpdateDeadline(ctx context.Context, id int, title *string, due *time.Time) (Deadline, error)
	DeleteDeadline(ctx context.Context, id int) error
	Ping(ctx context.Context) error
}

type server struct {
	store store
	loc   *time.Location
	now   func() time.Time
}

func newServer(st store, loc *time.Location, now func() time.Time) *server {
	return &server{store: st, loc: loc, now: now}
}

func (s *server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, requestLogger)

	r.Get("/healthz", s.handleHealth)
	r.Route("/api", func(r chi.Router) {
		r.Route("/cases", func(r chi.Router) {
			r.Get("/", s.handleListCases)
			r.Post("/", s.handleCreateCase)
			r.Delete("/{id}", s.handleDeleteCase)
			r.Post("/{id}/deadlines", s.handleCreateDeadline)
		})
		r.Route("/deadlines", func(r chi.Router) {
			r.Get("/", s.handleListDeadlines)
			r.Get("/conflicts", s.handleConflicts)
			r.Patch("/{id}", s.handleUpdateDeadline)
			r.Delete("/{id}", s.handleDeleteDeadline)
		})
	})
	return r
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"took", time.Since(start),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}

type casesResponse struct {
	Cases []Case `json:"cases"`
}

type createCaseRequest struct {
	Title     string          `json:"title"`
	Deadlines []deadlineInput `json:"deadlines"`
}

type deadlineInput struct {
	Title   string    `json:"title"`
	DueDate time.Time `json:"due_date"`
}

type createCaseResponse struct {
	Case      Case     `json:"case"`
	Deadlines []Ranked `json:"deadlines"`
}

type patchDeadlineRequest struct {
	Title   *string    `json:"title"`
	DueDate *time.Time `json:"due_date"`
}

type deadlinesResponse struct {
	Deadlines []Ranked `json:"deadlines"`
}

type conflictsResponse struct {
	Mode          string     `json:"mode"`
	WindowMinutes *int       `json:"window_minutes,omitempty"`
	Conflicts     []Conflict `json:"conflicts"`
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		slog.ErrorContext(r.Context(), "health check failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleListCases(w http.ResponseWriter, r *http.Request) {
	cases, err := s.store.ListCases(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, casesResponse{Cases: cases})
}

func (s *server) handleCreateCase(w http.ResponseWriter, r *http.Request) {
	var req createCaseRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	title, err := cleanTitle(req.Title)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Deadlines) > maxCaseDeadlines {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d deadlines per case", maxCaseDeadlines))
		return
	}
	deadlines := make([]NewDeadline, 0, len(req.Deadlines))
	for i, in := range req.Deadlines {
		d, err := cleanDeadline(in)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("deadline %d: %s", i, err))
			return
		}
		deadlines = append(deadlines, d)
	}

	created, added, err := s.store.CreateCase(r.Context(), title, deadlines)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, createCaseResponse{
		Case:      created,
		Deadlines: rank(added, s.now(), s.loc),
	})
}

func (s *server) handleDeleteCase(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.DeleteCase(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleCreateDeadline(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var in deadlineInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	d, err := cleanDeadline(in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	created, err := s.store.CreateDeadline(r.Context(), id, d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, rank([]Deadline{created}, s.now(), s.loc)[0])
}

func (s *server) handleListDeadlines(w http.ResponseWriter, r *http.Request) {
	order := r.URL.Query().Get("sort")
	if order == "" {
		order = sortDue
	}
	if order != sortDue && order != sortUrgency {
		writeError(w, http.StatusBadRequest, "sort must be urgency or due")
		return
	}

	deadlines, err := s.store.ListDeadlines(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ranked := rank(deadlines, s.now(), s.loc)
	sortRanked(ranked, order)
	writeJSON(w, http.StatusOK, deadlinesResponse{Deadlines: ranked})
}

func (s *server) handleConflicts(w http.ResponseWriter, r *http.Request) {
	window, err := parseWindow(r.URL.Query().Get("window"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	deadlines, err := s.store.ListDeadlines(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	now := s.now()
	resp := conflictsResponse{
		Mode:      "same_day",
		Conflicts: findConflicts(rank(deadlines, now, s.loc), now, s.loc, window),
	}
	if window > 0 {
		minutes := int(window.Minutes())
		resp.Mode = "window"
		resp.WindowMinutes = &minutes
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleUpdateDeadline(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req patchDeadlineRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Title == nil && req.DueDate == nil {
		writeError(w, http.StatusBadRequest, "give title or due_date")
		return
	}
	if req.Title != nil {
		title, err := cleanTitle(*req.Title)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.Title = &title
	}
	if req.DueDate != nil && req.DueDate.IsZero() {
		writeError(w, http.StatusBadRequest, "due_date is required")
		return
	}

	updated, err := s.store.UpdateDeadline(r.Context(), id, req.Title, req.DueDate)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rank([]Deadline{updated}, s.now(), s.loc)[0])
}

func (s *server) handleDeleteDeadline(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.DeleteDeadline(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fail is the one place a store error becomes a status code.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, errNotFound.Error())
	case errors.Is(err, errReadOnly):
		writeError(w, http.StatusConflict, errReadOnly.Error())
	default:
		slog.ErrorContext(r.Context(), "request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func pathID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 1 {
		return 0, errors.New("id must be a positive integer")
	}
	return id, nil
}

func cleanTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	if title == "" {
		return "", errors.New("title is required")
	}
	if utf8.RuneCountInString(title) > maxTitleRunes {
		return "", fmt.Errorf("title must be at most %d characters", maxTitleRunes)
	}
	return title, nil
}

func cleanDeadline(in deadlineInput) (NewDeadline, error) {
	title, err := cleanTitle(in.Title)
	if err != nil {
		return NewDeadline{}, err
	}
	if in.DueDate.IsZero() {
		return NewDeadline{}, errors.New("due_date is required")
	}
	return NewDeadline{Title: title, DueDate: in.DueDate}, nil
}

func parseWindow(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	window, err := time.ParseDuration(raw)
	if err != nil {
		return 0, errors.New("window must be a duration such as 36h")
	}
	if window <= 0 || window > maxWindow {
		return 0, errors.New("window must be greater than zero and at most 720h")
	}
	return window, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("request body is not valid json for this endpoint")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
