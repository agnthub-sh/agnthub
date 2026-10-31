// Package e2e is agnthub's end-to-end regression suite: it installs fixture
// repositories into each real Harness CLI inside a container, compares every
// result byte for byte with golden files and asks the Harness which agents it
// loads. The tests carry the e2e build tag and run with make e2e; see
// CONTRIBUTING.md.
package e2e
