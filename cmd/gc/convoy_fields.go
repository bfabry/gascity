package main

import (
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/ops"
)

// ConvoyFields is an alias for the shared ops type.
type ConvoyFields = ops.ConvoyFields

// applyConvoyFields delegates to ops.ApplyConvoyFields.
func applyConvoyFields(b *beads.Bead, fields ConvoyFields) {
	ops.ApplyConvoyFields(b, fields)
}

// setConvoyFields delegates to ops.SetConvoyFields.
func setConvoyFields(store beads.Store, id string, fields ConvoyFields) error {
	return ops.SetConvoyFields(store, id, fields)
}

// getConvoyFields delegates to ops.GetConvoyFields.
func getConvoyFields(b beads.Bead) ConvoyFields {
	return ops.GetConvoyFields(b)
}
