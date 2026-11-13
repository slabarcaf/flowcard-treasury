#!/usr/bin/env bash
# ==============================================================================
# FlowCard Frontend Distribution Checksum Helper
# ==============================================================================
# Computes a deterministic SHA-256 digest over relative file paths and bytes,
# ensuring the hash remains identical across different parent directory roots.
# Enforces that the target build directory exists and contains at least 1 file.
# Hashes all files (including any hidden files) so any tampering alters the hash.
# ==============================================================================
set -euo pipefail

TARGET_DIR="${1:-.dev/dist}"

if [[ ! -d "${TARGET_DIR}" ]]; then
  echo "ERROR: Target directory does not exist: ${TARGET_DIR}" >&2
  exit 1
fi

(
  cd "${TARGET_DIR}"
  FILE_COUNT="$(find . -type f | wc -l | tr -d ' ')"
  if [[ "${FILE_COUNT}" -eq 0 ]]; then
    echo "ERROR: Target build directory contains zero files: ${TARGET_DIR}" >&2
    exit 1
  fi
  # Hash all files deterministically over relative paths (./...) and contents
  find . -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}'
)
