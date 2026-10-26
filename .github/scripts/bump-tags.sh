#!/usr/bin/env bash
# Tags each Go module (<module>/vX.Y.Z) based on the conventional commits that
# touched its directory since its last tag:
#   feat                                      -> minor
#   fix, perf, refactor, chore, build, revert -> patch
#   type! or "BREAKING CHANGE:" in the body   -> major (minor while on v0)
#   docs, test, ci, style, anything else      -> no release
#
# DRY_RUN=1 prints the planned tags without creating or pushing them.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

breaking_re='^[a-z]+(\([^)]*\))?!:'
minor_re='^feat(\([^)]*\))?:'
patch_re='^(fix|perf|refactor|chore|build|revert)(\([^)]*\))?:'

new_tags=()
failed=0

for gomod in */go.mod; do
  module="${gomod%/go.mod}"
  last_tag=$(git tag --list "${module}/v*" --sort=-v:refname | head -n1)
  if [[ -z "$last_tag" ]]; then
    version="v0.0.0"
    range="HEAD"
  else
    version="${last_tag#"${module}/"}"
    range="${last_tag}..HEAD"
  fi

  level=0
  for sha in $(git log --no-merges --format=%H "$range" -- "$module"); do
    subject=$(git log -1 --format=%s "$sha")
    if [[ "$subject" =~ $breaking_re ]] || git log -1 --format=%b "$sha" | grep -qE '^BREAKING[ -]CHANGE:'; then
      level=3
    elif [[ "$subject" =~ $minor_re ]]; then
      ((level < 2)) && level=2
    elif [[ "$subject" =~ $patch_re ]]; then
      ((level < 1)) && level=1
    fi
  done

  if ((level == 0)); then
    echo "${module}: no releasable changes since ${last_tag:-the beginning}"
    continue
  fi

  IFS=. read -r major minor patch <<<"${version#v}"
  patch="${patch%%-*}"
  if ((level == 3 && major >= 1)); then
    echo "::error::${module}: breaking change needs a /v$((major + 1)) module path; tag it manually"
    failed=1
    continue
  fi
  if ((level == 3 || level == 2)); then
    minor=$((minor + 1))
    patch=0
  else
    patch=$((patch + 1))
  fi

  new_tag="${module}/v${major}.${minor}.${patch}"
  echo "${module}: ${last_tag:-none} -> ${new_tag}"
  if [[ -z "${DRY_RUN:-}" ]]; then
    git tag -a "$new_tag" -m "$new_tag" \
      -m "$(git log --no-merges --format='- %s' "$range" -- "$module")"
    new_tags+=("$new_tag")
  fi
done

if ((${#new_tags[@]} > 0)); then
  git push origin "${new_tags[@]}"
fi

exit "$failed"
