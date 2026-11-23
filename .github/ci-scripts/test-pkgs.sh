#!/usr/bin/env bash
# Print the import paths of the root-module packages that belong to a test group.
# CI and `make test-*` both use it, so a package can never fall between jobs.
#
#   unit-cmd        cmd/...
#   unit-plugins    exporter/... retriever/... notifier/...
#   unit-rest       every other package
#   unit-all        every package (the three groups above together)
#   integration     packages with docker-tagged tests that are NOT gopherstack e2e
#   e2e             packages with gopherstack e2e tests (files named *e2e*_test.go)
#   check           fail unless the unit-* shards are disjoint and cover `go list ./...`
#
# Run from anywhere; the script works on the root module.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

# Module path of the package in the root directory. `go list -m` would print every
# workspace module when a go.work file is active, which breaks the shard filters.
mod=$(go list -f '{{.Module.Path}}' .)
all_pkgs() { go list ./...; }

unit_cmd()     { all_pkgs | grep -E "^${mod}/cmd(/|$)" || true; }
unit_plugins() { all_pkgs | grep -E "^${mod}/(exporter|retriever|notifier)(/|$)" || true; }
unit_rest()    { all_pkgs | grep -vE "^${mod}/(cmd|exporter|retriever|notifier)(/|$)" || true; }

# Packages (import paths) having a docker-tagged _test.go file. $1 selects
# "e2e" (file name contains e2e) or "other" (it does not).
docker_pkgs() {
	local kind=$1 dirs
	dirs=$(grep -rlE --include='*_test.go' '^//go:build .*\bdocker\b' . \
		--exclude-dir=website --exclude-dir=vendor --exclude-dir=.git --exclude-dir=node_modules 2>/dev/null |
		while read -r f; do
			case "$kind:$(basename "$f")" in
			e2e:*e2e*) dirname "$f" ;;
			other:*e2e*) ;;
			other:*) dirname "$f" ;;
			esac
		done | sort -u)
	# Keep only directories that are packages of the root module (nested
	# modules such as rules/ and modules/core have their own go.mod).
	for d in $dirs; do
		go list -tags=docker "$d" 2>/dev/null || true
	done | sort -u
}

case "${1:-}" in
unit-cmd) unit_cmd ;;
unit-plugins) unit_plugins ;;
unit-rest) unit_rest ;;
unit-all) all_pkgs ;;
integration) docker_pkgs other ;;
e2e) docker_pkgs e2e ;;
check)
	expected=$(all_pkgs | sort)
	actual=$( { unit_cmd; unit_plugins; unit_rest; } | sort)
	if ! diff <(echo "$expected") <(echo "$actual") > /dev/null; then
		echo "::error::unit shards do not partition 'go list ./...'" >&2
		diff <(echo "$expected") <(echo "$actual") >&2 || true
		exit 1
	fi
	for g in integration e2e; do
		[[ -n "$("$0" "$g")" ]] || { echo "::error::group $g selects no package" >&2; exit 1; }
	done
	echo "OK: $(echo "$expected" | wc -l) packages, shards partition them; integration=$("$0" integration | wc -l) e2e=$("$0" e2e | wc -l)"
	;;
*)
	echo "usage: $0 unit-cmd|unit-plugins|unit-rest|unit-all|integration|e2e|check" >&2
	exit 2
	;;
esac
