// Package canopy implements the Canopy service module. Canopy is a framework
// demo service that exercises Grove's APIs and proves the framework's defaults,
// testing story, and upgrade path.
package canopy

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kusold/grove"
	"github.com/kusold/grove/migrate"
	"github.com/kusold/grove/tenancy"
)

// migrationFS holds the canopy service migrations. They are embedded so the
// compiled binary carries its schema and Grove can apply it at startup.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// canopyMigrations returns the canopy service migration source registered with
// Grove's migration registry.
func canopyMigrations() migrate.Source {
	return migrate.Source{
		Name: "canopy",
		FS:   migrationFS,
		Dir:  "migrations",
	}
}

// Module implements grove.Module for the Canopy service.
type Module struct{}

// Name returns the stable service identity used for logging and wiring.
func (Module) Name() string { return "canopy" }

// Register wires Canopy's routes into the Grove app.
func (Module) Register(_ context.Context, app *grove.App) error {
	app.HTTP().Route("/example", func(r chi.Router) {
		r.Get("/hello", helloHandler)

		// Tenant-required route group demonstrates grove.WithTenancy().
		// The global tenancy.Middleware resolves the tenant from headers.
		// tenancy.RequireMiddleware rejects requests when no tenant is present.
		r.Route("/whoami-tenant", func(r chi.Router) {
			r.Use(tenancy.RequireMiddleware())
			r.Get("/", whoamiTenantHandler)
		})

		// Tenant-scoped widget CRUD. The routes are registered only when the
		// Postgres capability is enabled, so HTTP-only wiring (and the tests
		// that exercise it) do not require a database. In production
		// (main.go) both Postgres and Tenancy are enabled, so the routes are
		// available and guarded by tenant-required middleware. All persistence
		// goes through db.TenantTx via pgxWidgetStore, so row-level security
		// enforces tenant isolation at the database boundary.
		if database, err := app.RequireDB(); err == nil {
			widgets := &widgetAPI{store: &pgxWidgetStore{db: database}}
			r.Route("/widgets", func(r chi.Router) {
				r.Use(tenancy.RequireMiddleware())
				r.Post("/", widgets.create)
				r.Get("/", widgets.list)
				r.Get("/{id}", widgets.get)
			})
		}
	})

	// Register canopy migrations when the capability is enabled. Production
	// wiring (main.go) always enables migrations, so this runs in every real
	// deployment. The conditional keeps tests that exercise HTTP-only behavior
	// from requiring a database.
	if reg, err := app.RequireMigrations(); err == nil {
		if err := reg.Register(canopyMigrations()); err != nil {
			return fmt.Errorf("register canopy migrations: %w", err)
		}
	}

	return nil
}

func helloHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"message": "hello from canopy",
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func whoamiTenantHandler(w http.ResponseWriter, r *http.Request) {
	tenant, err := tenancy.Require(r.Context())
	if err != nil {
		// This should not happen because RequireMiddleware ensures a tenant
		// is present, but fail closed if it does.
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"tenant_id":   tenant.ID,
		"tenant_slug": tenant.Slug,
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
