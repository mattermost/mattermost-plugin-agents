#!/usr/bin/env bash
# Builds a Mattermost enterprise server image from local checkouts so e2e specs can run against
# unreleased server changes:
#
#   e2e/scripts/build-local-mm-image.sh                       # tag: mm-notification-lab:local
#   MM_IMAGE=mm-notification-lab:local TESTCONTAINERS_HOST_OVERRIDE=localhost \
#     npx playwright test tests/notifications --project=chromium
#
# How it works (fast path): compile the linux/amd64 server binary with the enterprise build tags
# straight from the checkout and layer it, plus the checkout's server i18n and email templates,
# on top of a released Mattermost image (/mattermost/bin/mattermost). Everything else (client
# bundle, config defaults, fonts, mmctl, prepackaged plugins) comes from the base image. Set
# BUILD_WEBAPP=1 to also rebuild the webapp and replace /mattermost/client (slow, a few minutes).
#
# Usage: build-local-mm-image.sh [image-tag]
#
# Environment:
#   MM_REPO         Mattermost checkout                      (default /agent/repos/mattermost)
#   EE_REPO         Enterprise checkout; empty = team build  (default /agent/repos/enterprise)
#   BASE_IMAGE      Image to layer onto                      (default mattermost/mattermost-enterprise-edition:latest)
#   MM_LOCAL_TAG    Output tag when no argument is given     (default mm-notification-lab:local)
#   BUILD_WEBAPP    1 = rebuild webapp/channels and ship it as /mattermost/client (default 0)
#   BUILD_MMCTL     1 = also rebuild mmctl from the checkout (default 0, base image mmctl is used)
#   GO              go binary                                (default go)
#   KEEP_CONTEXT    1 = keep the temporary docker context dir for inspection (default 0)
#
# The checkout may carry uncommitted work; the image contains exactly what is on disk. The image
# is linux/amd64 only. A failed Go build usually means the checkout is mid-edit: fix or retry.

set -euo pipefail

MM_REPO="${MM_REPO:-/agent/repos/mattermost}"
EE_REPO="${EE_REPO-/agent/repos/enterprise}"
BASE_IMAGE="${BASE_IMAGE:-mattermost/mattermost-enterprise-edition:latest}"
TAG="${1:-${MM_LOCAL_TAG:-mm-notification-lab:local}}"
BUILD_WEBAPP="${BUILD_WEBAPP:-0}"
BUILD_MMCTL="${BUILD_MMCTL:-0}"
GO="${GO:-go}"
KEEP_CONTEXT="${KEEP_CONTEXT:-0}"

SERVER_DIR="${MM_REPO}/server"
[[ -d "${SERVER_DIR}/cmd/mattermost" ]] || { echo "error: ${SERVER_DIR}/cmd/mattermost not found (set MM_REPO)" >&2; exit 1; }

CONTEXT_DIR="$(mktemp -d)"
cleanup() {
    if [[ "${KEEP_CONTEXT}" == "1" ]]; then
        echo "docker context kept at ${CONTEXT_DIR}"
    else
        rm -rf "${CONTEXT_DIR}"
    fi
}
trap cleanup EXIT

BUILD_TAGS="production"
BUILD_ENTERPRISE_READY="false"
BUILD_HASH_ENTERPRISE="none"
WORK_FILE=""
if [[ -n "${EE_REPO}" ]]; then
    [[ -d "${EE_REPO}" ]] || { echo "error: EE_REPO ${EE_REPO} not found (set EE_REPO= for a team build)" >&2; exit 1; }
    BUILD_TAGS="enterprise production"
    BUILD_ENTERPRISE_READY="true"
    BUILD_HASH_ENTERPRISE="$(git -C "${EE_REPO}" rev-parse HEAD 2>/dev/null || echo none)"

    # The checkout's go.work points at ../../enterprise; generate one for an arbitrary EE_REPO.
    GO_VERSION_LINE="$(grep -m1 '^go ' "${SERVER_DIR}/go.work")"
    WORK_FILE="${CONTEXT_DIR}/go.work"
    cat >"${WORK_FILE}" <<EOF
${GO_VERSION_LINE}

use (
	${SERVER_DIR}
	${EE_REPO}
	${SERVER_DIR}/public
)
EOF
    [[ -f "${SERVER_DIR}/go.work.sum" ]] && cp "${SERVER_DIR}/go.work.sum" "${CONTEXT_DIR}/go.work.sum"
fi

BUILD_HASH="$(git -C "${MM_REPO}" rev-parse HEAD 2>/dev/null || echo unknown)"
MODEL_PKG="github.com/mattermost/mattermost/server/public/model"
LDFLAGS="-X \"${MODEL_PKG}.BuildNumber=local\""
LDFLAGS+=" -X \"${MODEL_PKG}.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
LDFLAGS+=" -X \"${MODEL_PKG}.BuildHash=${BUILD_HASH}\""
LDFLAGS+=" -X \"${MODEL_PKG}.BuildHashEnterprise=${BUILD_HASH_ENTERPRISE}\""
LDFLAGS+=" -X \"${MODEL_PKG}.BuildEnterpriseReady=${BUILD_ENTERPRISE_READY}\""

go_build() {
    local output="$1" package="$2"
    (
        cd "${SERVER_DIR}"
        export GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOFLAGS="-buildvcs=false"
        [[ -n "${WORK_FILE}" ]] && export GOWORK="${WORK_FILE}"
        "${GO}" build -trimpath -tags "${BUILD_TAGS}" -ldflags "${LDFLAGS}" -o "${output}" "${package}"
    )
}

echo "==> building mattermost (tags: ${BUILD_TAGS}) from ${SERVER_DIR}"
go_build "${CONTEXT_DIR}/mattermost" ./cmd/mattermost

if [[ "${BUILD_MMCTL}" == "1" ]]; then
    echo "==> building mmctl"
    go_build "${CONTEXT_DIR}/mmctl" ./cmd/mmctl
fi

mkdir -p "${CONTEXT_DIR}/i18n" "${CONTEXT_DIR}/templates"
cp -R "${SERVER_DIR}/i18n/." "${CONTEXT_DIR}/i18n/"
cp -R "${SERVER_DIR}/templates/." "${CONTEXT_DIR}/templates/"

if [[ "${BUILD_WEBAPP}" == "1" ]]; then
    echo "==> building webapp (slow)"
    (
        cd "${MM_REPO}/webapp"
        npm ci
        npm run build
    )
    mkdir -p "${CONTEXT_DIR}/client"
    cp -R "${MM_REPO}/webapp/channels/dist/." "${CONTEXT_DIR}/client/"
fi

{
    echo "FROM ${BASE_IMAGE}"
    echo "COPY --chown=2000:2000 mattermost /mattermost/bin/mattermost"
    [[ "${BUILD_MMCTL}" == "1" ]] && echo "COPY --chown=2000:2000 mmctl /mattermost/bin/mmctl"
    echo "COPY --chown=2000:2000 i18n/ /mattermost/i18n/"
    echo "COPY --chown=2000:2000 templates/ /mattermost/templates/"
    if [[ "${BUILD_WEBAPP}" == "1" ]]; then
        echo "COPY --chown=2000:2000 client/ /mattermost/client/"
    fi
    echo "LABEL org.opencontainers.image.revision=${BUILD_HASH}"
} >"${CONTEXT_DIR}/Dockerfile"

echo "==> docker build -> ${TAG} (base ${BASE_IMAGE})"
docker build --platform linux/amd64 -t "${TAG}" "${CONTEXT_DIR}"

echo "==> done: ${TAG}"
echo "    MM_IMAGE=${TAG} TESTCONTAINERS_HOST_OVERRIDE=localhost npx playwright test <spec> --project=chromium"
