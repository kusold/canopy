package main

import (
	"github.com/kusold/canopy/internal/canopy"
	"github.com/kusold/grove"
)

func main() {
	grove.Main(
		canopy.Module{},
		grove.WithHTTP(),
	)
}
