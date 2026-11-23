#!/usr/bin/env bash
# Usage: eval-deps-changed.sh <package> < changed-files
#
# Exits 0 when any changed file (one repo-relative path per line on stdin) can
# change the evals of <package>: a file in a directory of a repo package its
# test binary depends on, or a file that changes how evals build or run.
# Exits 1 otherwise.
set -euo pipefail

pkg=${1:?usage: eval-deps-changed.sh <package> < changed-files}
root=$(git rev-parse --show-toplevel)
cd "$root"

mapfile -t dirs < <(go list -deps -test -f '{{if not .Standard}}{{.Dir}}{{end}}' "$pkg" |
	sed -n "s|^$root/||p" | sort -u)

while IFS= read -r file; do
	[ -n "$file" ] || continue
	case "$file" in
	go.mod | go.sum | Makefile | .github/workflows/evals.yml | .github/workflows/pr-evals.yml | .github/scripts/eval-deps-changed.sh)
		echo "$file changes how evals run"
		exit 0
		;;
	esac
	for dir in "${dirs[@]}"; do
		if [[ "$file" == "$dir/"* ]]; then
			echo "$file is in $dir, which $pkg depends on"
			exit 0
		fi
	done
done

echo "No change affects $pkg"
exit 1
