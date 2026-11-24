#!/usr/bin/env bash
# Run Fuzz* tests for a bounded time.
# One `go test -fuzz` call accepts a single function, so each match is run
# on its own. FUZZTIME limits each function (default 10s). Go test runs
# until that duration elapses.
#
# Usage:
#   FUZZTIME=10s .github/ci-scripts/fuzz.sh ./rules ./   fuzz every package of the modules
#   .github/ci-scripts/fuzz.sh --list ./rules ./         print "<module> <dir>" for each package with fuzz tests
#   FUZZTIME=10s .github/ci-scripts/fuzz.sh --dir <module> <dir>        fuzz the targets of one package
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

fuzztime=${FUZZTIME:-10s}
gocmd=${GOCMD:-go}

run_go() {
	if [[ -n "${GOWORK_ENV:-}" ]]; then
		env "${GOWORK_ENV}" "$gocmd" "$@"
	else
		"$gocmd" "$@"
	fi
}

# Directories (relative to the module) holding Fuzz* functions.
fuzz_dirs() {
	local module=$1
	(
		cd "$module"
		# Skip vendor, VCS metadata, and nested modules so `./` does not
		# fuzz ./rules a second time.
		files=$(find . \( -name vendor -o -name .git \) -prune -o -type d ! -name . -exec test -f '{}/go.mod' \; -prune -o -name '*_test.go' -print)
		if [[ -n "$files" ]]; then
			matches=$(printf '%s\n' "$files" | xargs grep -l '^func Fuzz' || true)
			if [[ -n "$matches" ]]; then
				printf '%s\n' "$matches" | xargs -n1 dirname | sort -u
			fi
		fi
	)
}

fuzz_dir() {
	local module=$1 dir=$2
	(
		cd "$module"
		list_out=$(run_go test -list '^Fuzz' "$dir")
		names=$(printf '%s\n' "$list_out" | grep '^Fuzz' || true)
		for name in $names; do
			echo "→ ${module} ${dir} ${name} (${fuzztime})"
			run_go test -run='^$' -fuzz="^${name}$" -fuzztime="$fuzztime" "$dir"
		done
	)
}

if [[ $# -eq 0 ]]; then
	echo "usage: $0 [--list|--dir <module> <dir>|<module>...]" >&2
	exit 1
fi

case "$1" in
--list)
	shift
	for module in "$@"; do
		for dir in $(fuzz_dirs "$module"); do
			echo "${module} ${dir}"
		done
	done
	;;
--dir)
	[[ $# -eq 3 ]] || { echo "usage: $0 --dir <module> <dir>" >&2; exit 1; }
	fuzz_dir "$2" "$3"
	;;
*)
	for module in "$@"; do
		echo "→ Fuzzing ${module} (${fuzztime} each)"
		for dir in $(fuzz_dirs "$module"); do
			fuzz_dir "$module" "$dir"
		done
	done
	;;
esac
