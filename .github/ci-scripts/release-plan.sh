#!/usr/bin/env bash
# Plans a run of .github/workflows/release.yml: which version, whether to create the tag,
# whether it is a dry run. Inputs come from the environment (never interpolated into code):
#   EVENT_NAME         push | workflow_dispatch
#   REF, REF_NAME      github.ref (refs/heads/main, refs/tags/v0.1.0), github.ref_name
#   INPUT_BUMP         patch | minor | major     (workflow_dispatch)
#   INPUT_DRY_RUN      true | false              (workflow_dispatch)
#   INPUT_RELEASE_TAG  existing tag to re-publish, or empty (workflow_dispatch)
# It reads the tags of the git repository in the current directory and appends
# dry-run=, tag= and create-tag= to $GITHUB_OUTPUT. Exit 1 with an ::error:: on a refused plan.
# Tested by test_release_plan.py (make test-ci-scripts).
set -euo pipefail

out="${GITHUB_OUTPUT:-/dev/null}"
summary="${GITHUB_STEP_SUMMARY:-/dev/null}"

# vMAJOR.MINOR.PATCH with an optional -alpha.N, -beta.N or -rc.N suffix. Other suffixes are
# refused: they could collide with the -fips/-bookworm/-trixie image tags.
release_re='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.[0-9]+)?$'
stable_re='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

fail() { echo "::error::$*"; exit 1; }

dry=true
create=false
if [[ "${EVENT_NAME:?}" == push ]]; then
  dry=false
  tag="${REF_NAME:?}"
elif [[ -n "${INPUT_RELEASE_TAG:-}" ]]; then
  tag="$INPUT_RELEASE_TAG"
  [[ "${INPUT_DRY_RUN:-true}" == false ]] || fail "release_tag publishes a tag: set dry_run=false (a dry run of an existing tag is a plain dry run, leave release_tag empty)"
  dry=false
  [[ "$tag" =~ $release_re ]] || fail "$tag is not a release version (vMAJOR.MINOR.PATCH[-alpha.N|-beta.N|-rc.N])"
  git rev-parse -q --verify "refs/tags/$tag" > /dev/null || fail "tag $tag does not exist: release_tag never creates a tag"
  # The attestations record the commit of the run (OIDC claims), so the run must be made on
  # the tag itself for the provenance to name the commit that was built.
  [[ "${REF:-}" == "refs/tags/$tag" ]] || fail "release_tag needs the run to be started on the tag: pick $tag in 'Use workflow from'"
else
  [[ "${INPUT_DRY_RUN:-true}" == false ]] && dry=false
  if [[ "$dry" == false ]]; then
    # Only main may be released. The dry run is allowed from any ref so a branch can be tested.
    [[ "${REF:-}" == refs/heads/main ]] || fail "a release is cut from main only: pick main in 'Use workflow from'"
    create=true
  fi
  bump="${INPUT_BUMP:-patch}"
  # Latest stable tag, highest version first. Pre-release tags are ignored.
  latest=$(git tag --list 'v*' | grep -E "$stable_re" | sort -V | tail -1 || true)
  if [[ -z "$latest" ]]; then
    # First release: v0.1.0 whatever the bump (the owner's chosen first version).
    tag=v0.1.0
  else
    IFS=. read -r major minor patch <<< "${latest#v}"
    case "$bump" in
      major) tag="v$((major + 1)).0.0" ;;
      minor) tag="v${major}.$((minor + 1)).0" ;;
      patch) tag="v${major}.${minor}.$((patch + 1))" ;;
      *) fail "unknown bump '$bump' (patch, minor or major)" ;;
    esac
  fi
  # The Go module path has no /vN suffix: a tag at major >= 2 would be invisible to the
  # Go module proxy. A v2 needs a deliberate module path change first.
  [[ "$tag" =~ ^v[01]\. ]] || fail "refusing $tag: major >= 2 needs a /vN module path first"
  if git rev-parse -q --verify "refs/tags/$tag" > /dev/null; then
    fail "refusing to overwrite existing tag $tag"
  fi
  echo "Next version: $tag (latest stable tag: ${latest:-none}, bump: $bump)"
  {
    echo "### Release plan"
    echo "- latest stable tag: \`${latest:-none}\`"
    echo "- bump: \`$bump\`"
    echo "- next version: \`$tag\`"
    echo "- dry run: \`$dry\`"
  } >> "$summary"
fi

if [[ "$dry" == false ]] && ! [[ "$tag" =~ $release_re ]]; then
  fail "$tag is not a release version (vMAJOR.MINOR.PATCH[-alpha.N|-beta.N|-rc.N])"
fi
{
  echo "dry-run=$dry"
  echo "tag=$tag"
  echo "create-tag=$create"
} >> "$out"
