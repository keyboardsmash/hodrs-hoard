package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeDB satisfies Pinger. A nil err means "database is up".
type fakeDB struct{ err error }

func (f fakeDB) PingContext(context.Context) error { return f.err }

func newTestServer(t *testing.T) http.Handler {
	t.Helper() // report failures at the caller's line, not here
	return newTestServerWithDB(t, fakeDB{})
}

func newTestServerWithDB(t *testing.T, db Pinger) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := New("test", log, db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s.Handler()
}

func TestHealth(t *testing.T) {
	h := newTestServer(t)
	// httptest runs a handler in memory, without opening a network port.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body health
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" || body.Version != "test" || body.Database != "ok" {
		t.Errorf("body = %+v", body)
	}
}

func TestHealthDatabaseDown(t *testing.T) {
	h := newTestServerWithDB(t, fakeDB{err: errors.New("connection refused")})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body health
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "error" || body.Database != "unreachable" {
		t.Errorf("body = %+v", body)
	}
}

func TestIndex(t *testing.T) {
	h := newTestServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<title>Höðrs Hoard</title>") {
		t.Errorf("page does not contain the app title")
	}
}

func TestRouting(t *testing.T) {
	h := newTestServer(t)
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/nope", http.StatusNotFound},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}
