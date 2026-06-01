package canopy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/kusold/grove"
)

func TestModuleName(t *testing.T) {
	var m Module
	if got := m.Name(); got != "canopy" {
		t.Errorf("Module.Name() = %q, want %q", got, "canopy")
	}
}

func TestModuleRegister(t *testing.T) {
	t.Run("wires /example/hello into the app HTTP registry", func(t *testing.T) {
		app, err := grove.NewApp("canopy", grove.WithHTTP())
		if err != nil {
			t.Fatalf("NewApp() error: %v", err)
		}

		var m Module
		if err := m.Register(context.Background(), app); err != nil {
			t.Fatalf("Register() error: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/example/hello", nil)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		contentType := rec.Header().Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
		}

		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body["message"] != "hello from canopy" {
			t.Errorf("message = %q, want %q", body["message"], "hello from canopy")
		}
	})
}

func TestHelloHandler(t *testing.T) {
	t.Run("returns hello message", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/example/hello", nil)
		rec := httptest.NewRecorder()

		r := chi.NewRouter()
		r.Get("/example/hello", helloHandler)
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body["message"] != "hello from canopy" {
			t.Errorf("message = %q, want %q", body["message"], "hello from canopy")
		}
	})

	t.Run("returns 500 when response write fails", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/example/hello", nil)
		rec := &failingResponseWriter{ResponseRecorder: httptest.NewRecorder()}

		r := chi.NewRouter()
		r.Get("/example/hello", helloHandler)
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})
}

// failingResponseWriter wraps httptest.ResponseRecorder and fails on Write,
// allowing tests to exercise the json.Encode error path in helloHandler.
type failingResponseWriter struct {
	*httptest.ResponseRecorder
}

func (f *failingResponseWriter) Write(p []byte) (int, error) {
	return 0, errWriteFailed
}

var errWriteFailed = &writeError{}

type writeError struct{}

func (e *writeError) Error() string { return "write failed" }
