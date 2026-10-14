// Package data embeds the bundled model files.
package data

import "embed"

//go:embed models/*.toml
var Models embed.FS

// Vendors is the curated list of known vendor-specific VCP codes (hints, not applied automatically).
//
//go:embed vendors.toml
var Vendors []byte
