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
)

// Module implements grove.Module for the Canopy service.
type Module struct{}

// Name returns the stable service identity used for logging and wiring.
func (Module) Name() string { return "canopy" }

// Register wires Canopy's routes into the Grove app.
func (Module) Register(_ context.Context, app *grove.App) error {
	app.HTTP().Route("/example", func(r chi.Router) {
		r.Get("/hello", helloHandler)
	})
	return nil
}

func helloHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "hello from canopy",
	})
}
