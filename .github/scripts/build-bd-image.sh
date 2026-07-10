#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Usage: build-bd-image.sh OUTPUT

Builds the bd release pinned in deps.env from its exact tagged source commit,
with the image-only x/crypto and x/net security pins applied. The Gas City Go
toolchain is forced so the upstream module cannot silently select an older Go
patch release.
USAGE
}

output="${1:-}"
if [[ -z "$output" || $# -ne 1 ]]; then
  usage
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"

# shellcheck disable=SC1091
. "${repo_root}/deps.env"

for name in \
  BD_REPO \
  BD_VERSION \
  BD_IMAGE_SOURCE_REF \
  BD_IMAGE_X_CRYPTO_VERSION \
  BD_IMAGE_X_NET_VERSION \
  BD_IMAGE_GO_MOD_SHA256 \
  BD_IMAGE_GO_SUM_SHA256; do
  if [[ -z "${!name:-}" ]]; then
    echo "deps.env missing ${name}" >&2
    exit 1
  fi
done

if [[ ! "$BD_REPO" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "BD_REPO must be an owner/repository pair, got: ${BD_REPO}" >&2
  exit 1
fi
if [[ ! "$BD_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
  echo "BD_VERSION must be a v-prefixed release tag, got: ${BD_VERSION}" >&2
  exit 1
fi
if [[ ! "$BD_IMAGE_SOURCE_REF" =~ ^[0-9a-f]{40}$ ]]; then
  echo "BD_IMAGE_SOURCE_REF must be a full commit SHA, got: ${BD_IMAGE_SOURCE_REF}" >&2
  exit 1
fi
for value in "$BD_IMAGE_X_CRYPTO_VERSION" "$BD_IMAGE_X_NET_VERSION"; do
  if [[ ! "$value" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "image module pins must be v-prefixed semantic versions, got: ${value}" >&2
    exit 1
  fi
done
for value in "$BD_IMAGE_GO_MOD_SHA256" "$BD_IMAGE_GO_SUM_SHA256"; do
  if [[ ! "$value" =~ ^[0-9a-f]{64}$ ]]; then
    echo "image overlay digests must be lowercase SHA-256 values, got: ${value}" >&2
    exit 1
  fi
done

for command in git go install; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "${command} is required" >&2
    exit 1
  fi
done

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
src="${tmp}/beads"

git -C "$tmp" init --quiet beads
git -C "$src" remote add origin "https://github.com/${BD_REPO}.git"
git -C "$src" fetch --quiet --depth=1 --no-tags origin "refs/tags/${BD_VERSION}"
tag_commit="$(git -C "$src" rev-parse 'FETCH_HEAD^{commit}')"
if [[ "$tag_commit" != "$BD_IMAGE_SOURCE_REF" ]]; then
  echo "${BD_VERSION} peels to ${tag_commit}, expected ${BD_IMAGE_SOURCE_REF}" >&2
  exit 1
fi
git -C "$src" checkout --quiet --detach "$tag_commit"

# Resolve the toolchain while Gas City's go.mod is active. Without this
# override, Go's automatic toolchain selection follows bd's older `go` line and
# can reintroduce fixed standard-library vulnerabilities into the image.
gas_go_version="$(go -C "$repo_root" env GOVERSION)"
gas_go_root="$(go -C "$repo_root" env GOROOT)"
gas_go="${gas_go_root}/bin/go"
if [[ ! "$gas_go_version" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "could not resolve an exact Gas City Go toolchain: ${gas_go_version}" >&2
  exit 1
fi
if [[ ! -x "$gas_go" || "$($gas_go version | awk '{print $3}')" != "$gas_go_version" ]]; then
  echo "resolved Go binary does not match ${gas_go_version}: ${gas_go}" >&2
  exit 1
fi
host_goos="$(GOTOOLCHAIN=local "$gas_go" env GOOS)"
host_goarch="$(GOTOOLCHAIN=local "$gas_go" env GOARCH)"
if [[ "$host_goos" != "linux" || "$host_goarch" != "amd64" ]]; then
  echo "bd image builds currently require a native linux/amd64 runner, got ${host_goos}/${host_goarch}" >&2
  exit 1
fi

GOTOOLCHAIN=local GOWORK=off "$gas_go" -C "$src" get \
  "golang.org/x/crypto@${BD_IMAGE_X_CRYPTO_VERSION}" \
  "golang.org/x/net@${BD_IMAGE_X_NET_VERSION}"
GOTOOLCHAIN=local GOWORK=off "$gas_go" -C "$src" mod tidy
GOTOOLCHAIN=local GOWORK=off "$gas_go" -C "$src" mod verify

go_mod_sha="$(sha256sum "${src}/go.mod" | awk '{print $1}')"
go_sum_sha="$(sha256sum "${src}/go.sum" | awk '{print $1}')"
if [[ "$go_mod_sha" != "$BD_IMAGE_GO_MOD_SHA256" ]]; then
  echo "patched bd go.mod digest ${go_mod_sha}, expected ${BD_IMAGE_GO_MOD_SHA256}" >&2
  exit 1
fi
if [[ "$go_sum_sha" != "$BD_IMAGE_GO_SUM_SHA256" ]]; then
  echo "patched bd go.sum digest ${go_sum_sha}, expected ${BD_IMAGE_GO_SUM_SHA256}" >&2
  exit 1
fi

resolved_crypto="$(GOTOOLCHAIN=local GOWORK=off "$gas_go" -C "$src" list -m -f '{{.Version}}' golang.org/x/crypto)"
resolved_net="$(GOTOOLCHAIN=local GOWORK=off "$gas_go" -C "$src" list -m -f '{{.Version}}' golang.org/x/net)"
if [[ "$resolved_crypto" != "$BD_IMAGE_X_CRYPTO_VERSION" ]]; then
  echo "resolved golang.org/x/crypto ${resolved_crypto}, expected ${BD_IMAGE_X_CRYPTO_VERSION}" >&2
  exit 1
fi
if [[ "$resolved_net" != "$BD_IMAGE_X_NET_VERSION" ]]; then
  echo "resolved golang.org/x/net ${resolved_net}, expected ${BD_IMAGE_X_NET_VERSION}" >&2
  exit 1
fi

unexpected_changes="$(git -C "$src" status --short | awk '$2 != "go.mod" && $2 != "go.sum" { print }')"
if [[ -n "$unexpected_changes" ]]; then
  echo "security pinning changed files outside go.mod/go.sum:" >&2
  echo "$unexpected_changes" >&2
  exit 1
fi

version="${BD_VERSION#v}"
ldflags="-s -w -X main.Version=${version} -X main.Build=gr7n-secure.1 -X main.Commit=${BD_IMAGE_SOURCE_REF} -X main.Branch=${BD_VERSION}"
GOTOOLCHAIN=local GOWORK=off CGO_ENABLED=1 "$gas_go" -C "$src" build \
  -tags gms_pure_go \
  -mod=readonly \
  -trimpath \
  -buildvcs=false \
  -ldflags "$ldflags" \
  -o "${tmp}/bd" \
  ./cmd/bd

built_go_version="$("$gas_go" version "${tmp}/bd" | awk '{print $2}')"
if [[ "$built_go_version" != "$gas_go_version" ]]; then
  echo "bd was built with ${built_go_version}, expected ${gas_go_version}" >&2
  exit 1
fi
"${src}/scripts/verify-cgo.sh" "${tmp}/bd"

build_info="$("$gas_go" version -m "${tmp}/bd")"
for expected in \
  $'\tdep\tgolang.org/x/crypto\t'"${BD_IMAGE_X_CRYPTO_VERSION}" \
  $'\tdep\tgolang.org/x/net\t'"${BD_IMAGE_X_NET_VERSION}" \
  $'\tbuild\tCGO_ENABLED=1' \
  $'\tbuild\tGOARCH=amd64' \
  $'\tbuild\tGOOS=linux'; do
  if ! grep -Fq "$expected" <<<"$build_info"; then
    echo "bd build metadata is missing: ${expected}" >&2
    exit 1
  fi
done

mkdir -p "$(dirname "$output")"
install -m 0755 "${tmp}/bd" "$output"
"$output" version
