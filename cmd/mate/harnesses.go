package main

import "github.com/nguyenngocanh94/mate/internal/harness/catalog"

// harnesses are the harnesses this binary launches. cmd/mate is where the
// catalog is named; every package below it is handed this registry through
// its Deps (docs/plans/harness-registry-2026-09-30.md, section 3.4).
var harnesses = catalog.Default()
