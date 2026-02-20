#!/usr/bin/env bash
# Build the Applied kueue image and push to all environment OCIR repos.
#
# Usage:
#   1. Make your changes on the v0.15.3-applied branch
#   2. Bump VERSION below
#   3. Run this script from the repo root: ./applied/build-and-push.sh
set -euo pipefail

VERSION=2

# --- Derived values ---
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BRANCH="v0.15.3-applied"
TAG="${BRANCH}-${VERSION}"

OCIR_BASE="us-phoenix-1.ocir.io/idskhu5vqvtl"
BUILD_REGISTRY="${OCIR_BASE}"

# All environment-specific OCIR repos
ENV_REPOS=(
    "ml-infra-prod-phx-oci"
    "ml-infra-dev-phx-oci"
    "ml-infra-dev-pepe-oci"
    "ml-infra-dev-kermit-oci"
    "ml-infra-dev-harold-oci"
)

echo "=== Checking for existing images with tag ${TAG} ==="
EXISTING=()
for repo in "${ENV_REPOS[@]}"; do
    image="${OCIR_BASE}/${repo}/k8s/kueue:${TAG}"
    if docker manifest inspect "${image}" > /dev/null 2>&1; then
        EXISTING+=("${image}")
    fi
done

if [ ${#EXISTING[@]} -gt 0 ]; then
    echo "WARNING: Tag ${TAG} already exists in:"
    for img in "${EXISTING[@]}"; do
        echo "  - ${img}"
    done
    echo ""
    read -p "Overwrite? (y/N) " confirm
    if [[ "${confirm}" != "y" && "${confirm}" != "Y" ]]; then
        echo "Aborted. Bump VERSION and try again."
        exit 1
    fi
fi

GIT_COMMIT=$(git -C "$REPO_ROOT" rev-parse HEAD)
GIT_DIRTY=$(git -C "$REPO_ROOT" diff-index --quiet HEAD && echo "" || echo "-dirty")

echo ""
echo "=== Building kueue image ==="
echo "  Branch:     ${BRANCH}"
echo "  Version:    ${VERSION}"
echo "  Image tag:  ${TAG}"
echo "  Git commit: ${GIT_COMMIT}${GIT_DIRTY}"
echo ""

# Build the image locally (amd64 only)
cd "$REPO_ROOT"
make image-build \
    IMAGE_REGISTRY="${BUILD_REGISTRY}" \
    GIT_TAG="${TAG}" \
    PLATFORMS=linux/amd64 \
    PUSH=--load

SOURCE_IMAGE="${BUILD_REGISTRY}/kueue:${TAG}"

echo ""
echo "=== Pushing to all environment OCIR repos ==="
for repo in "${ENV_REPOS[@]}"; do
    dest="${OCIR_BASE}/${repo}/k8s/kueue:${TAG}"
    echo "  -> ${dest}"
    docker tag "${SOURCE_IMAGE}" "${dest}"
    docker push "${dest}"
done

echo ""
echo "=== Done ==="
echo "Image ${TAG} pushed to all environments."
echo "  Git commit: ${GIT_COMMIT}${GIT_DIRTY}"
echo "Update kueue_values.yaml tag to: ${TAG}"
