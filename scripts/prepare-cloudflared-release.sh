#!/usr/bin/env bash
set -euo pipefail

upstream_tag="${1:?usage: prepare-cloudflared-release.sh UPSTREAM_TAG DEFAULT_BRANCH}"
default_branch="${2:?usage: prepare-cloudflared-release.sh UPSTREAM_TAG DEFAULT_BRANCH}"
if [[ ! "$upstream_tag" =~ ^[0-9]{4}\.[0-9]+\.[0-9]+$ ]]; then
  echo "Invalid cloudflared release tag: $upstream_tag" >&2
  exit 1
fi
git check-ref-format "refs/heads/$default_branch"

stable_tag='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
tag=""
if ! git diff --quiet HEAD -- go.mod go.sum; then
  # Include unpublished tags when allocating a version so failed releases never
  # cause an existing tag to be reused or moved.
  while IFS= read -r candidate; do
    if [[ "$candidate" =~ $stable_tag ]]; then
      tag="v${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.$((10#${BASH_REMATCH[3]} + 1))"
      break
    fi
  done < <(git tag --list 'v*' --sort=-version:refname)
  tag="${tag:-v0.1.0}"

  git config user.name 'github-actions[bot]'
  git config user.email '41898282+github-actions[bot]@users.noreply.github.com'
  git add go.mod go.sum
  git commit -m "build(deps): update cloudflared to $upstream_tag"
  git tag -a "$tag" -m "cfui $tag (cloudflared $upstream_tag)"
  # If the default branch advanced, neither the branch nor the tag is pushed.
  # The next scheduled run checks out the new HEAD and retries the update.
  git push --atomic origin "HEAD:refs/heads/$default_branch" "refs/tags/$tag"
else
  # Resume a failed release when the dependencies are already up to date.
  while IFS= read -r candidate; do
    if [[ "$candidate" =~ $stable_tag ]]; then
      tag="$candidate"
      break
    fi
  done < <(git tag --points-at HEAD --sort=-version:refname)
  if [[ -n "$tag" ]]; then
    release_id="$(gh api "repos/{owner}/{repo}/releases" --paginate --jq ".[] | select(.tag_name == \"$tag\" and .draft == false) | .id")"
    if [[ -n "$release_id" ]]; then
      tag=""
    fi
  fi
fi

if [[ -n "$tag" ]]; then
  echo "Release ready: $tag"
else
  echo "No cloudflared update or pending release at HEAD"
fi
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  echo "tag=$tag" >> "$GITHUB_OUTPUT"
fi
