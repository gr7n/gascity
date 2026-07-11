#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
Usage: build-image-go-tools.sh OUTPUT_DIR

Builds the exact GitHub CLI, Dolt, and kubectl releases pinned in deps.env
from their immutable upstream source commits with Gas City's pinned Go image
toolchain. The outputs are linux/amd64 runtime-image inputs, not host installs.
USAGE
}

output_dir="${1:-}"
if [[ -z "$output_dir" || $# -ne 1 ]]; then
  usage
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"

# shellcheck disable=SC1091
. "${repo_root}/deps.env"

required=(
  GO_VERSION
  IMAGE_GO_BUILDER
  GH_REPO
  GH_VERSION
  GH_IMAGE_SOURCE_REF
  GH_IMAGE_MODULE_SUM
  GH_IMAGE_GO_MOD_SUM
  DOLT_REPO
  DOLT_VERSION
  DOLT_IMAGE_SOURCE_REF
  KUBECTL_REPO
  KUBECTL_VERSION
  KUBECTL_IMAGE_SOURCE_REF
)
for name in "${required[@]}"; do
  if [[ -z "${!name:-}" ]]; then
    echo "deps.env missing ${name}" >&2
    exit 1
  fi
done

if [[ ! "$GO_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "GO_VERSION must be an exact patch release, got: ${GO_VERSION}" >&2
  exit 1
fi
if [[ ! "$IMAGE_GO_BUILDER" =~ ^docker\.io/library/golang:${GO_VERSION}-bookworm@sha256:[0-9a-f]{64}$ ]]; then
  echo "IMAGE_GO_BUILDER must pin the official Go ${GO_VERSION} Bookworm amd64 manifest" >&2
  exit 1
fi
for repo in "$GH_REPO" "$DOLT_REPO" "$KUBECTL_REPO"; do
  if [[ ! "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
    echo "tool source must be an owner/repository pair, got: ${repo}" >&2
    exit 1
  fi
done
if [[ ! "$GH_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
   [[ ! "$DOLT_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
   [[ ! "$KUBECTL_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "runtime tool versions must be exact semantic releases" >&2
  exit 1
fi
for ref in "$GH_IMAGE_SOURCE_REF" "$DOLT_IMAGE_SOURCE_REF" "$KUBECTL_IMAGE_SOURCE_REF"; do
  if [[ ! "$ref" =~ ^[0-9a-f]{40}$ ]]; then
    echo "runtime tool source refs must be full lowercase commit SHAs, got: ${ref}" >&2
    exit 1
  fi
done
for sum in "$GH_IMAGE_MODULE_SUM" "$GH_IMAGE_GO_MOD_SUM"; do
  if [[ ! "$sum" =~ ^h1:[A-Za-z0-9+/]{43}=$ ]]; then
    echo "GitHub CLI module sums must be exact h1 SHA-256 values, got: ${sum}" >&2
    exit 1
  fi
done

for command in git go install sha256sum; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "${command} is required" >&2
    exit 1
  fi
done

gas_go_version="$(go -C "$repo_root" env GOVERSION)"
gas_go_root="$(go -C "$repo_root" env GOROOT)"
gas_go="${gas_go_root}/bin/go"
if [[ "$gas_go_version" != "go${GO_VERSION}" ]]; then
  echo "resolved Gas City toolchain ${gas_go_version}, expected go${GO_VERSION}" >&2
  exit 1
fi
if [[ ! -x "$gas_go" || "$($gas_go version | awk '{print $3}')" != "$gas_go_version" ]]; then
  echo "resolved Go binary does not match ${gas_go_version}: ${gas_go}" >&2
  exit 1
fi
host_goos="$(GOTOOLCHAIN=local "$gas_go" env GOOS)"
host_goarch="$(GOTOOLCHAIN=local "$gas_go" env GOARCH)"
if [[ "$host_goos" != "linux" || "$host_goarch" != "amd64" ]]; then
  echo "image tool builds require native linux/amd64, got ${host_goos}/${host_goarch}" >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fetch_release() {
  local repo="$1"
  local tag="$2"
  local expected_ref="$3"
  local name="$4"
  local src="${tmp}/${name}"

  git -C "$tmp" init --quiet "$name"
  git -C "$src" remote add origin "https://github.com/${repo}.git"
  git -C "$src" fetch --quiet --depth=1 --no-tags origin "refs/tags/${tag}"
  local tag_commit
  tag_commit="$(git -C "$src" rev-parse 'FETCH_HEAD^{commit}')"
  if [[ "$tag_commit" != "$expected_ref" ]]; then
    echo "${repo} ${tag} peels to ${tag_commit}, expected ${expected_ref}" >&2
    exit 1
  fi
  git -C "$src" checkout --quiet --detach "$tag_commit"
}

assert_go_binary() {
  local binary="$1"
  local expected_cgo="$2"
  local build_info
  local built_go_version

  built_go_version="$($gas_go version "$binary" | awk '{print $2}')"
  if [[ "$built_go_version" != "$gas_go_version" ]]; then
    echo "$(basename "$binary") was built with ${built_go_version}, expected ${gas_go_version}" >&2
    exit 1
  fi
  build_info="$($gas_go version -m "$binary")"
  for expected in \
    $'\tbuild\tCGO_ENABLED='"${expected_cgo}" \
    $'\tbuild\tGOARCH=amd64' \
    $'\tbuild\tGOOS=linux'; do
    if ! grep -Fq "$expected" <<<"$build_info"; then
      echo "$(basename "$binary") build metadata is missing: ${expected}" >&2
      exit 1
    fi
  done
}

# GitHub CLI: build the versioned, sumdb-verified release module. The source
# epoch and version are explicit; the release's checked-in OAuth defaults stay
# unchanged. The versioned module path makes Go record v2.96.0 rather than a
# checkout-derived pseudo-version that vulnerability scanners would
# incorrectly sort below the fixed upstream releases.
fetch_release "$GH_REPO" "v${GH_VERSION}" "$GH_IMAGE_SOURCE_REF" gh
gh_src="${tmp}/gh"
GOTOOLCHAIN=local "$gas_go" -C "$gh_src" mod verify
gh_module_json="$(
  GOTOOLCHAIN=local GOWORK=off "$gas_go" -C "$tmp" mod download -json \
    "github.com/${GH_REPO}/v2@v${GH_VERSION}"
)"
json_string_field() {
  local name="$1"
  sed -nE 's/^[[:space:]]*"'"${name}"'": "([^"]+)",?$/\1/p' <<<"$gh_module_json"
}
gh_module_sum="$(json_string_field Sum)"
gh_go_mod_sum="$(json_string_field GoModSum)"
gh_origin_vcs="$(json_string_field VCS)"
gh_origin_url="$(json_string_field URL)"
gh_origin_hash="$(json_string_field Hash)"
gh_origin_ref="$(json_string_field Ref)"
if [[ "$gh_module_sum" != "$GH_IMAGE_MODULE_SUM" ]] ||
   [[ "$gh_go_mod_sum" != "$GH_IMAGE_GO_MOD_SUM" ]] ||
   [[ "$gh_origin_vcs" != "git" ]] ||
   [[ "$gh_origin_url" != "https://github.com/${GH_REPO}" ]] ||
   [[ "$gh_origin_hash" != "$GH_IMAGE_SOURCE_REF" ]] ||
   [[ "$gh_origin_ref" != "refs/tags/v${GH_VERSION}" ]]; then
  echo "GitHub CLI module download is not bound to the reviewed release source" >&2
  exit 1
fi
gh_source_epoch="$(git -C "$gh_src" show -s --format=%ct HEAD)"
gh_build_date="$(date -u --date="@${gh_source_epoch}" +'%Y-%m-%d')"
gh_ldflags="-s -w"
gh_ldflags+=" -X github.com/cli/cli/v2/internal/build.Date=${gh_build_date}"
gh_ldflags+=" -X github.com/cli/cli/v2/internal/build.Version=${GH_VERSION}"
mkdir -p "${tmp}/gh-bin"
GOTOOLCHAIN=local \
  GOWORK=off \
  CGO_ENABLED=0 \
  SOURCE_DATE_EPOCH="$gh_source_epoch" \
  GOBIN="${tmp}/gh-bin" \
  "$gas_go" install \
    -trimpath \
    -buildvcs=false \
    -ldflags "$gh_ldflags" \
    "github.com/${GH_REPO}/v2/cmd/gh@v${GH_VERSION}"
if [[ "$("${tmp}/gh-bin/gh" --version | head -n 1)" != "gh version ${GH_VERSION} "* ]]; then
  echo "rebuilt gh does not report version ${GH_VERSION}" >&2
  exit 1
fi
assert_go_binary "${tmp}/gh-bin/gh" 0
gh_module_record="$(printf '\tmod\tgithub.com/cli/cli/v2\tv%s' "$GH_VERSION")"
if ! "$gas_go" version -m "${tmp}/gh-bin/gh" | grep -Fq "$gh_module_record"; then
  echo "rebuilt gh is missing module version v${GH_VERSION}" >&2
  exit 1
fi

# Dolt: keep the exact v2.1.10 application source, but use the pure-Go regex
# implementation so the security rebuild does not introduce a second ICU ABI.
# gozstd still requires native CGO; the pinned builder includes the compiler.
fetch_release "$DOLT_REPO" "v${DOLT_VERSION}" "$DOLT_IMAGE_SOURCE_REF" dolt
dolt_src="${tmp}/dolt"
GOTOOLCHAIN=local "$gas_go" -C "${dolt_src}/go" mod verify
GOTOOLCHAIN=local \
  GOWORK=off \
  CGO_ENABLED=1 \
  "$gas_go" -C "${dolt_src}/go" build \
    -mod=readonly \
    -trimpath \
    -buildvcs=true \
    -tags 'gms_pure_go,timetzdata' \
    -ldflags '-s -w' \
    -o "${tmp}/dolt-bin" \
    ./cmd/dolt
if [[ "$("${tmp}/dolt-bin" version | head -n 1)" != "dolt version ${DOLT_VERSION}" ]]; then
  echo "rebuilt dolt does not report version ${DOLT_VERSION}" >&2
  exit 1
fi
assert_go_binary "${tmp}/dolt-bin" 1
dolt_revision_record="$(printf '\tbuild\tvcs.revision=%s' "$DOLT_IMAGE_SOURCE_REF")"
if ! "$gas_go" version -m "${tmp}/dolt-bin" | grep -Fq "$dolt_revision_record"; then
  echo "rebuilt dolt is missing exact source revision ${DOLT_IMAGE_SOURCE_REF}" >&2
  exit 1
fi

# kubectl: Kubernetes vendors its complete dependency graph. Build from that
# exact tree and embed deterministic release metadata derived from the commit.
fetch_release "$KUBECTL_REPO" "$KUBECTL_VERSION" "$KUBECTL_IMAGE_SOURCE_REF" kubernetes
kubectl_src="${tmp}/kubernetes"
kubectl_source_epoch="$(git -C "$kubectl_src" show -s --format=%ct HEAD)"
kubectl_build_date="$(date -u --date="@${kubectl_source_epoch}" +'%Y-%m-%dT%H:%M:%SZ')"
kubectl_version="${KUBECTL_VERSION#v}"
kubectl_major="${kubectl_version%%.*}"
kubectl_minor_patch="${kubectl_version#*.}"
kubectl_minor="${kubectl_minor_patch%%.*}"
kubectl_ldflags="-s -w"
kubectl_ldflags+=" -X k8s.io/component-base/version.gitMajor=${kubectl_major}"
kubectl_ldflags+=" -X k8s.io/component-base/version.gitMinor=${kubectl_minor}"
kubectl_ldflags+=" -X k8s.io/component-base/version.gitVersion=${KUBECTL_VERSION}"
kubectl_ldflags+=" -X k8s.io/component-base/version.gitCommit=${KUBECTL_IMAGE_SOURCE_REF}"
kubectl_ldflags+=" -X k8s.io/component-base/version.gitTreeState=clean"
kubectl_ldflags+=" -X k8s.io/component-base/version.buildDate=${kubectl_build_date}"
GOTOOLCHAIN=local \
  CGO_ENABLED=0 \
  "$gas_go" -C "$kubectl_src" build \
    -mod=vendor \
    -trimpath \
    -buildvcs=false \
    -ldflags "$kubectl_ldflags" \
    -o "${tmp}/kubectl-bin" \
    ./cmd/kubectl
kubectl_bin="${tmp}/kubectl-bin"
kubectl_version_json="$("$kubectl_bin" version --client=true -o json)"
for expected in \
  '"gitVersion": "'"${KUBECTL_VERSION}"'"' \
  '"gitCommit": "'"${KUBECTL_IMAGE_SOURCE_REF}"'"' \
  '"gitTreeState": "clean"' \
  '"goVersion": "'"${gas_go_version}"'"'; do
  if ! grep -Fq "$expected" <<<"$kubectl_version_json"; then
    echo "rebuilt kubectl metadata is missing: ${expected}" >&2
    exit 1
  fi
done
assert_go_binary "${tmp}/kubectl-bin" 0

mkdir -p "$output_dir"
install -m 0755 "${tmp}/gh-bin/gh" "${output_dir}/gh"
install -m 0755 "${tmp}/dolt-bin" "${output_dir}/dolt"
install -m 0755 "${tmp}/kubectl-bin" "${output_dir}/kubectl"
sha256sum "${output_dir}/gh" "${output_dir}/dolt" "${output_dir}/kubectl"
