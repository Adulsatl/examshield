#!/usr/bin/env bash
set -euo pipefail

# Build .deb for ExamShield EDU agent (Linux amd64)
# Requirements: golang, dpkg-deb, bash

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")"/../.. && pwd)"
PKG_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ORIGINAL_STAGE_DIR="${PKG_DIR}/stage"
VERSION="0.1.0"
ARCH="amd64"
PKG_NAME="examshield-agent_${VERSION}_${ARCH}.deb"

# Locate Go (works on Linux, WSL, and Git Bash on Windows)
GO_BIN="$(command -v go || true)"
if [[ -z "${GO_BIN}" ]]; then
  # Try common Windows paths under Git Bash/MSYS
  if [[ -x "/c/Program Files/Go/bin/go.exe" ]]; then
    GO_BIN="/c/Program Files/Go/bin/go.exe"
  elif [[ -x "/c/Users/${USERNAME}/AppData/Local/Programs/Go/bin/go.exe" ]]; then
    GO_BIN="/c/Users/${USERNAME}/AppData/Local/Programs/Go/bin/go.exe"
  fi
fi
if [[ -z "${GO_BIN}" ]]; then
  echo "Error: Go toolchain not found on PATH. Please install Go or add it to PATH." >&2
  exit 1
fi

# Decide where to stage the package contents.
# On WSL/Windows-mounted paths, chmod may be ignored; use a Linux temp dir instead.
BUILD_STAGE_DIR="${ORIGINAL_STAGE_DIR}"
if grep -qi microsoft /proc/version 2>/dev/null || [[ "${PKG_DIR}" == /mnt/* ]]; then
  BUILD_STAGE_DIR="$(mktemp -d)"
fi

rm -rf "${ORIGINAL_STAGE_DIR}"
mkdir -p "${BUILD_STAGE_DIR}"

# Copy DEBIAN control files
mkdir -p "${BUILD_STAGE_DIR}/DEBIAN"
cp -r "${PKG_DIR}/DEBIAN/"* "${BUILD_STAGE_DIR}/DEBIAN/"

# Copy systemd service file
mkdir -p "${BUILD_STAGE_DIR}/lib/systemd/system"
cp "${PKG_DIR}/lib/systemd/system/examshield-agent.service" "${BUILD_STAGE_DIR}/lib/systemd/system/"

# Build Linux binary
BIN_DIR="${BUILD_STAGE_DIR}/usr/local/bin"
mkdir -p "${BIN_DIR}"
(
  cd "${ROOT_DIR}/agent/cmd/agent"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${GO_BIN}" build -o "${BIN_DIR}/examshield-agent" .
)
chmod 0755 "${BIN_DIR}/examshield-agent"

# Ensure maintainer scripts are executable
chmod 0755 "${BUILD_STAGE_DIR}/DEBIAN/postinst"

# Fix permissions for control directory and files (WSL/DrvFs may default to 0777)
chmod 0755 "${BUILD_STAGE_DIR}/DEBIAN"
if [[ -f "${BUILD_STAGE_DIR}/DEBIAN/control" ]]; then
  chmod 0644 "${BUILD_STAGE_DIR}/DEBIAN/control"
fi

# Build the .deb
(
  cd "${PKG_DIR}"
  dpkg-deb --root-owner-group --build "${BUILD_STAGE_DIR}" "${PKG_NAME}"
)

# Mirror staged contents back to project for inspection and clean up temp
if [[ "${BUILD_STAGE_DIR}" != "${ORIGINAL_STAGE_DIR}" ]]; then
  rm -rf "${ORIGINAL_STAGE_DIR}"
  mkdir -p "${ORIGINAL_STAGE_DIR}"
  cp -a "${BUILD_STAGE_DIR}/." "${ORIGINAL_STAGE_DIR}/"
  rm -rf "${BUILD_STAGE_DIR}"
fi

echo "Built ${PKG_DIR}/${PKG_NAME}"
