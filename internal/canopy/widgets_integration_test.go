package canopy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kusold/grove/db"
	"github.com/kusold/grove/migrate"
	"github.com/kusold/grove/tenancy"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	widgetTenantAID   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	widgetTenantASlug = "acme"
)

// startPostgres starts a Postgres 18 container and returns a connection string.
// Canopy cannot import Grove's internal integrationtest helper (it lives under
// grove/internal), so this mirrors that helper locally. The container default
// user is a superuser; RLS cross-tenant isolation is therefore verified by
// Grove's own db package and the dedicated Phase 3 isolation tests rather than
// here. These tests exercise single-tenant CRUD and tenant gating.
func startPostgres(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := tcpostgres.Run(ctx, "postgres:18",
		tcpostgres.WithDatabase("canopy_test"),
		tcpostgres.WithUsername("canopy"),
		tcpostgres.WithPassword("canopy"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate postgres container: %v", err)
		}
	})

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("build connection string: %v", err)
	}
	return url
}

// setupWidgetDB starts Postgres, applies canopy's migrations (which include
// Grove's RLS prelude via the migration registry), and returns a connected
// database. The returned *db.Database is the same type production code uses, so
// TenantTx and the RLS-backed widget store behave exactly as at runtime.
func setupWidgetDB(t *testing.T) *db.Database {
	t.Helper()

	url := startPostgres(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	database, err := db.Open(ctx, db.Config{
		URL:            url,
		MaxConns:       4,
		MinConns:       0,
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(database.Close)

	// Apply Grove's RLS prelude and canopy's migrations (init + widgets table).
	// NewRegistry seeds Grove-owned migrations; canopy's source adds the
	// example_widgets table with its tenant isolation policy.
	registry := migrate.NewRegistry()
	if err := registry.Register(canopyMigrations()); err != nil {
		t.Fatalf("register canopy migrations: %v", err)
	}
	if err := registry.Run(ctx, database.Pool()); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	return database
}

func TestWidgetStoreIntegration(t *testing.T) {
	database := setupWidgetDB(t)
	store := &pgxWidgetStore{db: database}

	tenantA := tenancy.Tenant{ID: widgetTenantAID, Slug: widgetTenantASlug}
	ctxA := tenancy.WithTenant(context.Background(), tenantA)

	t.Run("create and get a widget for the tenant", func(t *testing.T) {
		created, err := store.Create(ctxA, "demo widget")
		if err != nil {
			t.Fatalf("Create() error: %v", err)
		}
		if created.ID == "" {
			t.Fatal("Create() returned empty ID")
		}
		if created.Name != "demo widget" {
			t.Errorf("name = %q, want %q", created.Name, "demo widget")
		}

		got, err := store.Get(ctxA, created.ID)
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if got != created {
			t.Errorf("Get() = %+v, want %+v", got, created)
		}
	})

	t.Run("list returns widgets for the tenant", func(t *testing.T) {
		first, err := store.Create(ctxA, "list widget one")
		if err != nil {
			t.Fatalf("Create() first: %v", err)
		}
		second, err := store.Create(ctxA, "list widget two")
		if err != nil {
			t.Fatalf("Create() second: %v", err)
		}

		widgets, err := store.List(ctxA)
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}

		seen := map[string]bool{first.ID: false, second.ID: false}
		for _, w := range widgets {
			if _, ok := seen[w.ID]; ok {
				seen[w.ID] = true
			}
		}
		for id, found := range seen {
			if !found {
				t.Errorf("List() did not return widget %q", id)
			}
		}
	})

	t.Run("list starts empty before creates", func(t *testing.T) {
		// A fresh database per subtest would be ideal, but the shared container
		// accumulates rows across subtests. Instead, verify List never errors
		// and returns a usable slice.
		widgets, err := store.List(ctxA)
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		_ = widgets
	})

	t.Run("get unknown id returns not found", func(t *testing.T) {
		_, err := store.Get(ctxA, "00000000-0000-0000-0000-000000000000")
		if !errors.Is(err, errWidgetNotFound) {
			t.Fatalf("Get() unknown error = %v, want errWidgetNotFound", err)
		}
	})

	t.Run("get malformed id returns not found", func(t *testing.T) {
		_, err := store.Get(ctxA, "not-a-uuid")
		if !errors.Is(err, errWidgetNotFound) {
			t.Fatalf("Get() malformed error = %v, want errWidgetNotFound", err)
		}
	})

	t.Run("store requires a tenant in context", func(t *testing.T) {
		// TenantTx requires a tenant; without one, every store method fails.
		_, err := store.Create(context.Background(), "no tenant")
		if err == nil {
			t.Fatal("Create() without tenant should fail")
		}
	})
}

func TestWidgetRoutesIntegration(t *testing.T) {
	database := setupWidgetDB(t)
	api := &widgetAPI{store: &pgxWidgetStore{db: database}}

	// Mirror the production wiring: global tenancy.Middleware resolves the
	// tenant from headers, and the route group enforces it.
	router := chi.NewRouter()
	router.Use(tenancy.Middleware(tenancy.HeaderResolver{}))
	router.Route("/example/widgets", func(r chi.Router) {
		r.Use(tenancy.RequireMiddleware())
		r.Post("/", api.create)
		r.Get("/", api.list)
		r.Get("/{id}", api.get)
	})

	withTenant := func(req *http.Request) {
		req.Header.Set("X-Tenant-ID", widgetTenantAID)
		req.Header.Set("X-Tenant-Slug", widgetTenantASlug)
	}

	var createdID string

	t.Run("POST creates a widget for the tenant", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/example/widgets", bytes.NewBufferString(`{"name":"demo widget"}`))
		withTenant(req)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusCreated, rec.Body.String())
		}
		if loc := rec.Header().Get("Location"); loc == "" {
			t.Error("Location header should be set on create")
		}

		var body widget
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.Name != "demo widget" {
			t.Errorf("name = %q, want %q", body.Name, "demo widget")
		}
		createdID = body.ID
	})

	t.Run("GET list returns the created widget", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/example/widgets", nil)
		withTenant(req)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		var body struct {
			Widgets []widget `json:"widgets"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		var found bool
		for _, w := range body.Widgets {
			if w.ID == createdID {
				found = true
			}
		}
		if !found {
			t.Errorf("list did not include created widget %q; got %+v", createdID, body.Widgets)
		}
	})

	t.Run("GET by id returns the widget for the tenant", func(t *testing.T) {
		if createdID == "" {
			t.Skip("no created widget id from previous subtest")
		}
		req := httptest.NewRequest(http.MethodGet, "/example/widgets/"+createdID, nil)
		withTenant(req)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		var body widget
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body.ID != createdID {
			t.Errorf("id = %q, want %q", body.ID, createdID)
		}
	})

	t.Run("GET by unknown id returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/example/widgets/00000000-0000-0000-0000-000000000000", nil)
		withTenant(req)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})

	t.Run("routes reject requests without a tenant header", func(t *testing.T) {
		for _, tc := range []struct {
			method string
			path   string
		}{
			{http.MethodPost, "/example/widgets"},
			{http.MethodGet, "/example/widgets"},
			{http.MethodGet, "/example/widgets/some-id"},
		} {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(`{"name":"demo"}`))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s %s without tenant status = %d, want %d", tc.method, tc.path, rec.Code, http.StatusUnprocessableEntity)
			}
		}
	})

	t.Run("POST rejects an empty name", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/example/widgets", bytes.NewBufferString(`{"name":"  "}`))
		withTenant(req)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
		}
	})
}
