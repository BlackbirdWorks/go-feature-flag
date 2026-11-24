module github.com/blackbirdworks/semaphore/modules/core

go 1.27.4

require (
	github.com/diegoholiveira/jsonlogic/v3 v3.11.0
	github.com/google/go-cmp v0.7.0
	github.com/nikunjy/rules v1.5.0
	github.com/stretchr/testify v1.12.1
)

// github.com/nikunjy/rules is built from the maintained fork github.com/go-feature-flag/rules, which lives in
// the rules/ directory of this repository (see rules/README.md for the pinned upstream commit).
// It adds proper semver support (prerelease comparison, see https://github.com/thomaspoignant/go-feature-flag/issues/4736)
// and rejects invalid rule syntax at parse time.
replace github.com/nikunjy/rules => ../../rules

require (
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	go.yaml.in/yaml/v3 v3.0.6 // indirect
	golang.org/x/exp v0.0.0-20261121134650-e1094eeca1b5 // indirect
)
