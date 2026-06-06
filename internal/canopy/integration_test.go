package canopy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kusold/grove"
	"github.com/kusold/grove/apperr"
	"github.com/kusold/grove/httpx"
)

// integrationTestApp creates a fully wired Grove App with the Canopy Module
// registered, suitable for in-process testing without binding a network port.
func integrationTestApp(t *testing.T, opts ...grove.Option) *grove.App {
	t.Helper()

	allOpts := []grove.Option{grove.WithHTTP()}
	allOpts = append(allOpts, opts...)

	app, err := grove.NewApp("canopy", allOpts...)
	if err != nil {
		t.Fatalf("NewApp() error: %v", err)
	}

	var m Module
	if err := m.Register(context.Background(), app); err != nil {
		t.Fatalf("Module.Register() error: %v", err)
	}

	return app
}

func TestIntegration_ExampleHello(t *testing.T) {
	t.Run("returns HTTP 200", func(t *testing.T) {
		app := integrationTestApp(t)

		req := httptest.NewRequest(http.MethodGet, "/example/hello", nil)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("returns application/json content type", func(t *testing.T) {
		app := integrationTestApp(t)

		req := httptest.NewRequest(http.MethodGet, "/example/hello", nil)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		ct := rec.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
		}
	})

	t.Run("returns expected JSON message", func(t *testing.T) {
		app := integrationTestApp(t)

		req := httptest.NewRequest(http.MethodGet, "/example/hello", nil)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body["message"] != "hello from canopy" {
			t.Errorf("message = %q, want %q", body["message"], "hello from canopy")
		}
	})
}

func TestIntegration_RouteNotFound(t *testing.T) {
	t.Run("returns 404 for unknown routes", func(t *testing.T) {
		app := integrationTestApp(t)

		req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})
}

func TestIntegration_MethodNotAllowed(t *testing.T) {
	t.Run("returns 405 for wrong method on registered route", func(t *testing.T) {
		app := integrationTestApp(t)

		req := httptest.NewRequest(http.MethodPost, "/example/hello", nil)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
		}
	})
}

func TestIntegration_WhoamiTenant(t *testing.T) {
	t.Run("returns tenant ID and slug when tenant headers are present", func(t *testing.T) {
		app := integrationTestApp(t, grove.WithTenancy())

		req := httptest.NewRequest(http.MethodGet, "/example/whoami-tenant", nil)
		req.Header.Set("X-Tenant-ID", "00000000-0000-0000-0000-000000000001")
		req.Header.Set("X-Tenant-Slug", "acme")
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}

		ct := rec.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("Content-Type = %q, want %q", ct, "application/json")
		}

		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body["tenant_id"] != "00000000-0000-0000-0000-000000000001" {
			t.Errorf("tenant_id = %q, want %q", body["tenant_id"], "00000000-0000-0000-0000-000000000001")
		}
		if body["tenant_slug"] != "acme" {
			t.Errorf("tenant_slug = %q, want %q", body["tenant_slug"], "acme")
		}
	})

	t.Run("returns tenant_required error when no tenant headers present", func(t *testing.T) {
		app := integrationTestApp(t, grove.WithTenancy())

		req := httptest.NewRequest(http.MethodGet, "/example/whoami-tenant", nil)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
		}

		var body apperr.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != "tenant_required" {
			t.Errorf("code = %q, want %q", body.Error.Code, "tenant_required")
		}
	})

	t.Run("non-tenant routes still work without tenant headers", func(t *testing.T) {
		app := integrationTestApp(t, grove.WithTenancy())

		req := httptest.NewRequest(http.MethodGet, "/example/hello", nil)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

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

	t.Run("returns 400 when only X-Tenant-ID header is present", func(t *testing.T) {
		app := integrationTestApp(t, grove.WithTenancy())

		req := httptest.NewRequest(http.MethodGet, "/example/whoami-tenant", nil)
		req.Header.Set("X-Tenant-ID", "t1")
		// X-Tenant-Slug is missing — HeaderResolver returns an error
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}

		var body apperr.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != "invalid_tenant" {
			t.Errorf("code = %q, want %q", body.Error.Code, "invalid_tenant")
		}
	})

	t.Run("returns 400 when only X-Tenant-Slug header is present", func(t *testing.T) {
		app := integrationTestApp(t, grove.WithTenancy())

		req := httptest.NewRequest(http.MethodGet, "/example/whoami-tenant", nil)
		req.Header.Set("X-Tenant-Slug", "acme")
		// X-Tenant-ID is missing — HeaderResolver returns an error
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}

		var body apperr.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != "invalid_tenant" {
			t.Errorf("code = %q, want %q", body.Error.Code, "invalid_tenant")
		}
	})

	t.Run("returns tenant_required when both headers are whitespace-only", func(t *testing.T) {
		app := integrationTestApp(t, grove.WithTenancy())

		req := httptest.NewRequest(http.MethodGet, "/example/whoami-tenant", nil)
		req.Header.Set("X-Tenant-ID", "  ")
		req.Header.Set("X-Tenant-Slug", "\t")
		// Both whitespace-only: resolver returns (Tenant{}, false, nil),
		// which means no tenant in context, so RequireMiddleware rejects.
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
		}

		var body apperr.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != "tenant_required" {
			t.Errorf("code = %q, want %q", body.Error.Code, "tenant_required")
		}
	})

	t.Run("tenant-required error response includes request_id when present", func(t *testing.T) {
		app := integrationTestApp(t, grove.WithTenancy())

		req := httptest.NewRequest(http.MethodGet, "/example/whoami-tenant", nil)
		// Inject request ID into context to simulate request ID middleware
		ctx := httpx.WithRequestID(req.Context(), "req-integration-001")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
		}

		var body apperr.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != "tenant_required" {
			t.Errorf("code = %q, want %q", body.Error.Code, "tenant_required")
		}
		if body.Error.RequestID != "req-integration-001" {
			t.Errorf("request_id = %q, want %q", body.Error.RequestID, "req-integration-001")
		}
	})

	t.Run("invalid-tenant error response includes request_id when present", func(t *testing.T) {
		app := integrationTestApp(t, grove.WithTenancy())

		req := httptest.NewRequest(http.MethodGet, "/example/whoami-tenant", nil)
		req.Header.Set("X-Tenant-ID", "t1")
		// X-Tenant-Slug missing triggers resolver error
		ctx := httpx.WithRequestID(req.Context(), "req-integration-002")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		app.HTTP().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}

		var body apperr.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
		if body.Error.Code != "invalid_tenant" {
			t.Errorf("code = %q, want %q", body.Error.Code, "invalid_tenant")
		}
		if body.Error.RequestID != "req-integration-002" {
			t.Errorf("request_id = %q, want %q", body.Error.RequestID, "req-integration-002")
		}
	})
}
