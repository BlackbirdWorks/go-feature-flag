#!/usr/bin/env bash
# Pull container images in parallel, each with retries. A throttled or dropped
# pull in the middle of a test shows up as a container start timeout, so pull
# everything once up front. Failures only warn: the test will retry the pull.
# Usage: pull-images.sh <image>...
set -uo pipefail
for img in "$@"; do
	(
		for i in 1 2 3; do
			docker pull -q "$img" > /dev/null && exit 0
			sleep $((i * 5))
		done
		echo "::warning::pre-pull of $img failed"
	) &
done
wait
docker images
df -h /
