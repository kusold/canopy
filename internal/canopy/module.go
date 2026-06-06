// Package canopy implements the Canopy service module. Canopy is a framework
// demo service that exercises Grove's APIs and proves the framework's defaults,
// testing story, and upgrade path.
package canopy

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kusold/grove"
	"github.com/kusold/grove/tenancy"
)

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
	})
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
