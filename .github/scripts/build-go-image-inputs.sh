#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: build-go-image-inputs.sh OUTPUT_DIR" >&2
}

output_dir="${1:-}"
if [[ -z "$output_dir" || $# -ne 1 ]]; then
  usage
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mkdir -p "$output_dir"
"${script_dir}/build-bd-image.sh" "${output_dir}/bd"
"${script_dir}/build-image-go-tools.sh" "${output_dir}/image-tools"
