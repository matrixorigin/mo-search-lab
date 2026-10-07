#!/usr/bin/env bash
set -euo pipefail

version=${1:-v0.8.3}
target_os=${2:-linux}
target_arch=${3:-amd64}
for component in "$version" "$target_os" "$target_arch"; do
  [[ "$component" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || { echo 'invalid release name' >&2; exit 1; }
done

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
mkdir -p dist
name="mo-search-lab-${version}-${target_os}-${target_arch}"
stage=$(mktemp -d "$repo_dir/dist/.release-stage-XXXXXX")
trap 'rm -rf "$stage"' EXIT
bundle="$stage/$name"
mkdir -p "$bundle/packs"

GOWORK=off CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
  "${GO:-go}" build -mod=readonly -trimpath -ldflags="-s -w -X main.version=$version" \
  -o "$bundle/mo-search-lab" ./cmd/mo-search-lab
cp README.md LICENSE "$bundle/"
cp -R docs "$bundle/"
mkdir -p "$bundle/cmd/mo-search-lab"
cp cmd/mo-search-lab/README.md "$bundle/cmd/mo-search-lab/"
cp -R cmd/mo-search-lab/testdata/smoke "$bundle/packs/smoke"
(
  cd "$bundle"
  find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS
)
tar -C "$stage" -czf "$stage/$name.tar.gz" "$name"
mv "$stage/$name.tar.gz" "dist/$name.tar.gz"
(
  cd dist
  sha256sum "$name.tar.gz" > "$name.tar.gz.sha256"
)
echo "release: dist/$name.tar.gz"
