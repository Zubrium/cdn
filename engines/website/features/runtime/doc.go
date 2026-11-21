// Package runtime implements the target static-lifecycle feature ABI.
//
// The ABI is intentionally unversioned: "static-lifecycle" is the only accepted
// value of manifest.runtime.abi. A feature build supplies a local ESM entry that
// exports render, hydrate, update and dispose. This package owns the host-side
// contract: manifest and asset validation, secret-free FeatureContext building,
// per-placement lifecycle ordering with cancellation and timeout, dependency
// resolution against an immutable lock, and publication fan-out with independent
// per-target CAS. JavaScript execution itself is delegated to an injected
// Executor so that no module in this repository depends on an embedded JS engine.
package runtime
