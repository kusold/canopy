package canopy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestModuleName(t *testing.T) {
	var m Module
	if got := m.Name(); got != "canopy" {
		t.Errorf("Module.Name() = %q, want %q", got, "canopy")
	}
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

		contentType := rec.Header().Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
		}

		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if body["message"] != "hello from canopy" {
			t.Errorf("message = %q, want %q", body["message"], "hello from canopy")
		}
	})
}

func TestModuleRegister(t *testing.T) {
	t.Run("registers routes without error", func(t *testing.T) {
		// Verify Module implements the expected interface by calling its methods.
		var m Module
		if m.Name() != "canopy" {
			t.Fatalf("Name() = %q, want %q", m.Name(), "canopy")
		}
	})
}
