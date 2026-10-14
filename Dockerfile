# syntax=docker/dockerfile:1
# BASE_REGISTRY lets air-gapped or rate-limited environments pull base images
# from a mirror, e.g. --build-arg BASE_REGISTRY=mirror.gcr.io/library
ARG BASE_REGISTRY=docker.io/library
# The UI and API stages run on the build machine's own architecture and
# cross-compile, so multi-arch releases don't compile Go under QEMU emulation.
# ---- UI ----
FROM --platform=$BUILDPLATFORM ${BASE_REGISTRY}/node:24-alpine AS ui
WORKDIR /ui
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# ---- API (single static binary with the UI embedded) ----
FROM --platform=$BUILDPLATFORM ${BASE_REGISTRY}/golang:1.27-alpine AS api
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN rm -rf web/dist
COPY --from=ui /ui/dist ./web/dist
ARG VERSION=dev
# GitHub repository (owner/name) the release was built from: instances update
# from it automatically, without any configuration.
ARG UPDATE_REPO=
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags="-s -w -X github.com/portdock/portdock/internal/api.Version=${VERSION} -X github.com/portdock/portdock/internal/api.DefaultUpdateRepo=${UPDATE_REPO}" \
    -o /out/portdock ./cmd/portdock && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pdctl ./cmd/pdctl

# ---- runtime ----
FROM ${BASE_REGISTRY}/alpine:3
# wireguard-tools, iproute2 and iptables: the image also runs the agent of the
# private network on each server (portdock wg-agent).
RUN apk add --no-cache docker-cli docker-cli-buildx docker-cli-compose git openssh-client ca-certificates tzdata bash curl wireguard-tools-wg iproute2 iptables
# Nixpacks (static musl build) so the local server can use the Nixpacks build pack.
ARG NIXPACKS_VERSION=v1.41.0
ARG TARGETARCH
RUN arch=$([ "$TARGETARCH" = "arm64" ] && echo aarch64 || echo x86_64) && \
    curl -fsSL "https://github.com/railwayapp/nixpacks/releases/download/${NIXPACKS_VERSION}/nixpacks-${NIXPACKS_VERSION}-${arch}-unknown-linux-musl.tar.gz" \
    | tar -xz -C /usr/local/bin nixpacks && nixpacks --version
COPY --from=api /out/portdock /usr/local/bin/portdock
COPY --from=api /out/pdctl /usr/local/bin/pdctl
ENV PORTDOCK_DATA_DIR=/data/portdock \
    PORTDOCK_ADDR=:8000
EXPOSE 8000
VOLUME /data/portdock
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1:8000/api/health || exit 1
ENTRYPOINT ["portdock"]
