package webdist

import "embed"

// FS is replaced by `make web` with the compiled Svelte app before release builds.
//
//go:embed dist/*
var FS embed.FS
