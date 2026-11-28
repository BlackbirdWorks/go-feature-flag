// Package docstest holds the tests that run the examples of the documentation: the bootstrap files and control-plane
// documents that the pages show are valid, and the getting-started page is run command by command against the built
// binaries.
//
// A page marks a fenced block for the tests with a line `<!-- docs-test: run -->` (a shell block that the end-to-end
// test runs) or `<!-- docs-test: output -->` (the text the preceding run block prints) just before the fence.
package docstest
