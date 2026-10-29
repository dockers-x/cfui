#!/usr/bin/env bash
set -euo pipefail

release_tag="${1:?usage: update-cloudflared.sh RELEASE_TAG}"
# cloudflared publishes calendar-versioned releases. Refuse branch names,
# pseudo-versions and prereleases so an automated update cannot select HEAD.
if [[ ! "$release_tag" =~ ^v?[0-9]{4}\.[0-9]+\.[0-9]+$ ]]; then
  echo "Expected a stable cloudflared release tag (for example 2026.10.0)" >&2
  exit 1
fi
module=github.com/cloudflare/cloudflared
quic_module=github.com/quic-go/quic-go

upstream_download="$(go mod download -json "$module@$release_tag")" || {
  download_status=$?
  echo "Failed to download cloudflared release $release_tag" >&2
  printf '%s\n' "$upstream_download" >&2
  exit "$download_status"
}
target_version="$(jq -er '.Version' <<< "$upstream_download")"
upstream_mod="$(jq -er '.GoMod' <<< "$upstream_download")"
# Let Go parse go.mod, including single-line and block directives.
upstream_json="$(go mod edit -json "$upstream_mod")"
quic_version="$(jq -er --arg module "$quic_module" '
  [.Require[]? | select(.Path == $module) | .Version]
  | if length == 1 then .[0] else error("missing or ambiguous quic-go requirement") end
' <<< "$upstream_json")"
quic_replacements="$(jq -c --arg module "$quic_module" '
  [.Replace[]? | select(.Old.Path == $module) | .Old.Version //= "" | .New.Version //= ""]
' <<< "$upstream_json")"
# Local replacement paths belong to the upstream checkout and cannot be used here.
if ! jq -e 'all(.[]; .New.Version != "")' <<< "$quic_replacements" >/dev/null; then
  echo "cloudflared declares a local quic-go replacement; review the update manually" >&2
  exit 1
fi

current_json="$(go mod edit -json)"
current_version="$(jq -r --arg module "$module" '.Require[]? | select(.Path == $module) | .Version' <<< "$current_json")"
current_quic_version="$(jq -r --arg module "$quic_module" '.Require[]? | select(.Path == $module) | .Version' <<< "$current_json")"
current_replacements="$(jq -c --arg module "$quic_module" '[.Replace[]? | select(.Old.Path == $module) | .Old.Version //= "" | .New.Version //= ""]' <<< "$current_json")"
if [[ "$target_version" == "$current_version" && "$quic_version" == "$current_quic_version" && "$quic_replacements" == "$current_replacements" ]]; then
  echo "cloudflared is already at $release_tag ($current_version)"
  exit 0
fi

# Drop stale replacements, including version-specific entries, before mirroring
# the selected release. An upstream release without a replace removes our fork.
while IFS= read -r old; do
  go mod edit "-dropreplace=$old"
done < <(jq -r '.[] | .Old.Path + (if .Old.Version == "" then "" else "@" + .Old.Version end)' <<< "$current_replacements")
while IFS= read -r replacement; do
  go mod edit "-replace=$replacement"
done < <(jq -r '.[] | .Old.Path + (if .Old.Version == "" then "" else "@" + .Old.Version end) + "=" + .New.Path + "@" + .New.Version' <<< "$quic_replacements")

go get "$module@$release_tag" "$quic_module@$quic_version"
go mod tidy
echo "Updated cloudflared to $release_tag ($target_version) and synchronized quic-go ($quic_version)"
