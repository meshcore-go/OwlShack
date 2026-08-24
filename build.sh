#!/usr/bin/env bash
# Build the OwlShack binary: compile the React SPA, then build the static Go
# binary that embeds it. Mirrors .github/workflows/release.yml.
#
#   ./build.sh                       # -> ./OwlShack, version from git
#   OUTPUT=dist/owlshack ./build.sh  # custom output path
#   VERSION=v1.2.3 ./build.sh        # pin the stamped version
set -euo pipefail

# Always run from the repo root, regardless of the caller's cwd.
cd "$(dirname "${BASH_SOURCE[0]}")"

OUTPUT="${OUTPUT:-OwlShack}"
# Stamped into the binary (buildinfo.Version, shown in the UI). Defaults to a
# git description, falling back to "dev" outside a checkout.
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"

echo ">> Building frontend"
pushd web/frontend >/dev/null
npm ci                  # lockfile-exact + reproducible (matches CI); use `npm install` for fast local iteration
npm run build
popd >/dev/null

# Stamp the build version into the embedded SW so its bytes change every release:
# that's what triggers the browser's SW update (and the reload toast) and rotates
# the cache name. App freshness is already handled by hashed /assets/ URLs.
sed -i "s/__BUILD_VERSION__/${VERSION}/" web/frontend/dist/sw.js

echo ">> Building backend -> ${OUTPUT} (version ${VERSION})"
go mod download
CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags "-s -w -X github.com/meshcore-go/OwlShack/internal/buildinfo.Version=${VERSION}" \
  -o "${OUTPUT}" .

echo ">> Done: ${OUTPUT} ($(du -h "${OUTPUT}" | cut -f1))"
