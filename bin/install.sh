#!/usr/bin/env bash
# Installs the flock cli from the public GitHub releases of seagalia/flock.
# Usage: install.sh [tag]   (the latest release when no tag is given)

set -euo pipefail

REPO="seagalia/flock"

if [ $# -gt 0 ]; then
  TAG=$1
else
  TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
fi
if [ -z "${TAG}" ]; then
  echo "Could not find the latest flock release" >&2
  exit 1
fi

VERSION=${TAG#v}
OS=linux
if echo "${OSTYPE}" | grep -q darwin; then
  OS=darwin
fi

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *)
    echo "Unsupported architecture $(uname -m)" >&2
    exit 1
    ;;
esac

ARCHIVE="flock_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/v${VERSION}"

echo "Downloading version ${VERSION} of flock cli..."

WORK_DIR=$(mktemp -d)
trap 'rm -rf "${WORK_DIR}"' EXIT

curl -fsSL -o "${WORK_DIR}/${ARCHIVE}" "${BASE_URL}/${ARCHIVE}"
curl -fsSL -o "${WORK_DIR}/checksums.txt" "${BASE_URL}/checksums.txt"

EXPECTED=$(awk -v name="${ARCHIVE}" '$2 == name { print $1 }' "${WORK_DIR}/checksums.txt")
if [ -z "${EXPECTED}" ]; then
  echo "No checksum for ${ARCHIVE} in checksums.txt" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "${WORK_DIR}/${ARCHIVE}" | awk '{ print $1 }')
else
  ACTUAL=$(shasum -a 256 "${WORK_DIR}/${ARCHIVE}" | awk '{ print $1 }')
fi
if [ "${EXPECTED}" != "${ACTUAL}" ]; then
  echo "Checksum mismatch for ${ARCHIVE}: expected ${EXPECTED}, got ${ACTUAL}" >&2
  exit 1
fi

tar xz --strip-components=1 -C . -f "${WORK_DIR}/${ARCHIVE}" "flock_${VERSION}_${OS}_${ARCH}/flock"
echo ""

printf "\e[32mInstallation success\e[0m, please follow next instruction:\n"

if echo "${OSTYPE}" | grep -q darwin; then
  printf 'Run \e[33mmv "flock" "/usr/local/bin/flock"\e[0m\n'
else
  printf 'Run \e[33msudo mv "flock" "/usr/local/bin/flock"\e[0m\n'
fi

if echo "${SHELL:-}" | grep -q zsh; then
  printf 'Add to your \e[33m~/.zshrc\e[0m this: \e[34meval "$(flock hook -c zsh)"\e[0m\n'
elif echo "${SHELL:-}" | grep -q bash; then
  printf 'Add to your \e[33m~/.bashrc\e[0m this: \e[34meval "$(flock hook -c bash)"\e[0m\n'
else
  printf 'Add to your rc file this: \e[34meval "$(flock hook)"\e[0m\n'
fi
echo ""
printf 'You can now use flock after \n'
