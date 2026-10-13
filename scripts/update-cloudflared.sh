#!/usr/bin/env bash
set -euo pipefail

release_tag="${1:?usage: update-cloudflared.sh RELEASE_TAG}"
module=github.com/cloudflare/cloudflared

target_version="$(go list -m -f '{{.Version}}' "$module@$release_tag")"
current_version="$(go list -m -f '{{.Version}}' "$module")"
if [[ "$target_version" == "$current_version" ]]; then
  echo "cloudflared is already at $release_tag ($current_version)"
  exit 0
fi

upstream_mod="$(go mod download -json "$module@$release_tag" | jq -r '.GoMod')"
quic_replacement="$(awk '$1 == "replace" && $2 == "github.com/quic-go/quic-go" && $3 == "=>" { print $4 "@" $5 }' "$upstream_mod")"
if [[ -z "$quic_replacement" ]]; then
  echo "cloudflared no longer declares a single-line quic-go replacement; review the update manually" >&2
  exit 1
fi

go get "$module@$release_tag"
go mod edit "-replace=github.com/quic-go/quic-go=$quic_replacement"
go mod tidy
echo "Updated cloudflared to $release_tag ($target_version) and quic-go to $quic_replacement"
