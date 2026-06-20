package canopy

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/kusold/grove"
	"github.com/kusold/grove/migrate"
	"github.com/kusold/grove/tenancy"
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

	t.Run("skips migration registration when capability is not enabled", func(t *testing.T) {
		// HTTP-only wiring must not require Postgres or migrations. This lets
		// HTTP-focused tests run without a database.
		app, err := grove.NewApp("canopy", grove.WithHTTP())
		if err != nil {
			t.Fatalf("NewApp() error: %v", err)
		}

		var m Module
		if err := m.Register(context.Background(), app); err != nil {
			t.Fatalf("Register() error: %v", err)
		}

		if _, err := app.RequireMigrations(); err == nil {
			t.Fatal("RequireMigrations() unexpectedly succeeded without WithMigrations()")
		}
	})

	t.Run("registers canopy migrations when capability is enabled", func(t *testing.T) {
		app, err := grove.NewApp(
			"canopy",
			grove.WithHTTP(),
			grove.WithPostgres(),
			grove.WithMigrations(),
		)
		if err != nil {
			t.Fatalf("NewApp() error: %v", err)
		}

		var m Module
		if err := m.Register(context.Background(), app); err != nil {
			t.Fatalf("Register() error: %v", err)
		}

		reg, err := app.RequireMigrations()
		if err != nil {
			t.Fatalf("RequireMigrations() error: %v", err)
		}

		sources := reg.Sources()
		var canopySource *migrate.Source
		for i := range sources {
			if sources[i].Name == "canopy" {
				canopySource = &sources[i]
				break
			}
		}
		if canopySource == nil {
			t.Fatalf("canopy migration source not registered; sources = %+v", sources)
		}
		if canopySource.Dir != "migrations" {
			t.Errorf("canopy source Dir = %q, want %q", canopySource.Dir, "migrations")
		}
		if canopySource.FS == nil {
			t.Error("canopy source FS is nil")
		}
	})
}

func TestCanopyMigrations(t *testing.T) {
	t.Run("returns a source named canopy rooted at migrations", func(t *testing.T) {
		source := canopyMigrations()
		if source.Name != "canopy" {
			t.Errorf("Name = %q, want %q", source.Name, "canopy")
		}
		if source.Dir != "migrations" {
			t.Errorf("Dir = %q, want %q", source.Dir, "migrations")
		}
		if source.FS == nil {
			t.Fatal("FS is nil; embed directive likely did not match any files")
		}
	})

	t.Run("embeds at least one SQL migration file", func(t *testing.T) {
		source := canopyMigrations()
		matches, err := fs.Glob(source.FS, source.Dir+"/*.sql")
		if err != nil {
			t.Fatalf("glob embedded migrations: %v", err)
		}
		if len(matches) == 0 {
			t.Fatal("no embedded migration files found; expected at least one")
		}
	})
}

// TestExampleWidgetsMigration guards the security-critical elements of the
// tenant-scoped widgets migration. The full runtime RLS isolation behavior is
// exercised by a separate Phase 3 issue; this test ensures the migration source
// carries the table, forced row-level security, and a tenant isolation policy
// that defers to grove.current_tenant_id(). It runs without a database so it
// stays CI-safe and fails loudly if any of those guarantees are dropped.
func TestExampleWidgetsMigration(t *testing.T) {
	source := canopyMigrations()
	matches, err := fs.Glob(source.FS, source.Dir+"/*example_widgets.sql")
	if err != nil {
		t.Fatalf("glob example_widgets migration: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 example_widgets migration, got %d: %v", len(matches), matches)
	}

	data, err := fs.ReadFile(source.FS, matches[0])
	if err != nil {
		t.Fatalf("read embedded example_widgets migration: %v", err)
	}
	sql := string(data)

	// Each assertion maps to an acceptance criterion from kusold/grove#54.
	for _, want := range []string{
		"-- +goose Up",
		"create table example_widgets",
		"id uuid primary key",
		"tenant_id uuid not null",
		"name text not null",
		"created_at timestamptz not null default now()",
		"alter table example_widgets enable row level security",
		"alter table example_widgets force row level security",
		"create policy example_widgets_tenant_isolation on example_widgets",
		"using (tenant_id = grove.current_tenant_id())",
		"with check (tenant_id = grove.current_tenant_id())",
		"-- +goose Down",
		"drop table if exists example_widgets",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("example_widgets migration missing %q", want)
		}
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

func TestWhoamiTenantHandler(t *testing.T) {
	t.Run("returns tenant ID and slug from context", func(t *testing.T) {
		tenant := tenancy.Tenant{ID: "00000000-0000-0000-0000-000000000001", Slug: "acme"}

		req := httptest.NewRequest(http.MethodGet, "/example/whoami-tenant", nil)
		req = req.WithContext(tenancy.WithTenant(req.Context(), tenant))
		rec := httptest.NewRecorder()

		r := chi.NewRouter()
		r.Get("/example/whoami-tenant", whoamiTenantHandler)
		r.ServeHTTP(rec, req)

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
		if body["tenant_id"] != tenant.ID {
			t.Errorf("tenant_id = %q, want %q", body["tenant_id"], tenant.ID)
		}
		if body["tenant_slug"] != tenant.Slug {
			t.Errorf("tenant_slug = %q, want %q", body["tenant_slug"], tenant.Slug)
		}
	})

	t.Run("returns 500 when no tenant in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/example/whoami-tenant", nil)
		rec := httptest.NewRecorder()

		r := chi.NewRouter()
		r.Get("/example/whoami-tenant", whoamiTenantHandler)
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})

	t.Run("returns 500 when response write fails", func(t *testing.T) {
		tenant := tenancy.Tenant{ID: "t1", Slug: "acme"}
		req := httptest.NewRequest(http.MethodGet, "/example/whoami-tenant", nil)
		req = req.WithContext(tenancy.WithTenant(req.Context(), tenant))
		rec := &failingResponseWriter{ResponseRecorder: httptest.NewRecorder()}

		r := chi.NewRouter()
		r.Get("/example/whoami-tenant", whoamiTenantHandler)
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
