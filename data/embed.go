// Package data embeds the bundled model files.
package data

import "embed"

//go:embed models/*.toml
var Models embed.FS
