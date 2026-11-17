# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26.11-trixie@sha256:cff05dd6b81c901f2e8249eb6f91c69a60451d0f12c26bfef08e476a85b32d4d AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG BUILD_TIME=unknown
ARG RELEASE_REPOSITORY=
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -buildvcs=false -trimpath \
    -ldflags="-s -w -X pulso.local/pulso/internal/version.Version=${VERSION} -X pulso.local/pulso/internal/version.BuildTime=${BUILD_TIME} -X pulso.local/pulso/internal/version.ReleaseRepository=${RELEASE_REPOSITORY}" \
    -o /out/pulso ./cmd/pulso

FROM python:3.11-slim-trixie@sha256:07d4824036af339fa1b6ef769cc99d9c10a141769538b52b3b597c84f7a7b535 AS python311

FROM node:24-trixie-slim@sha256:af02d57b57719b9657a45199a79264a9437940cd55b3c7b3fc99615654f124a0 AS runtime-base
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl python3 python3-venv tini \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 pulso \
    && useradd --uid 10001 --gid pulso --home-dir /var/lib/pulso --no-create-home --shell /bin/sh pulso \
    && install -d -m 0700 -o pulso -g pulso /var/lib/pulso \
    && install -d -m 0750 -o pulso -g pulso /apps
COPY --from=build /out/pulso /usr/local/bin/pulso
COPY LICENSE /usr/share/doc/pulso/LICENSE
ENV PULSO_CONTAINER=1
USER pulso:pulso
WORKDIR /apps
EXPOSE 9615
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD ["curl", "--fail", "--silent", "--show-error", "--max-time", "3", "--output", "/dev/null", "http://127.0.0.1:9615/api/auth/status"]
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/pulso", "--data-dir", "/var/lib/pulso"]
CMD ["daemon", "run", "--web", "--web-listen", "0.0.0.0:9615"]

# Optional exact interpreter for scripts that invoke python3.11.
# Keep the Debian Python for OS tooling; /usr/local/bin takes precedence for applications.
FROM runtime-base AS runtime-python311
USER root
COPY --from=python311 /usr/local/ /usr/local/
RUN ldconfig \
    && python3.11 -c "import ssl,sqlite3,bz2,lzma,ctypes,readline,uuid,venv; assert ssl.OPENSSL_VERSION" \
    && python3.11 -m pip --version \
    && node --version
USER pulso:pulso

# The default build remains the standard Node.js + Debian Python image.
FROM runtime-base AS runtime
