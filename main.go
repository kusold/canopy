// Package main is the Canopy service entrypoint. It wires the canopy module to
// Grove with the capabilities the service depends on.
package main

import (
	"github.com/kusold/canopy/internal/canopy"
	"github.com/kusold/grove"
)

// options returns the Grove capabilities enabled for the Canopy service. Each
// option corresponds to a framework feature the service depends on. The list is
// the authoritative wiring for the service and is exercised by tests so missing
// capabilities are caught before runtime.
func options() []grove.Option {
	opts := []grove.Option{
		grove.WithHTTP(),
		grove.WithTenancy(),
	}
	opts = append(opts, grove.WithPostgres())
	opts = append(opts, grove.WithMigrations())
	return opts
}

func main() {
	grove.Main(canopy.Module{}, options()...)
}
