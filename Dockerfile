# syntax=docker/dockerfile:1.7

# ---- builder ---------------------------------------------------------------
# The toolchain must match the `go` directive in go.mod.
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder

WORKDIR /src

# Dependencies first so a source-only change doesn't re-download the module graph.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# VERSION is stamped into main.version of both binaries.
ARG VERSION=dev
# Provided automatically by buildx for the target platform.
ARG TARGETOS
ARG TARGETARCH

# CGO_ENABLED=0: modernc.org/sqlite is pure Go, so this is a static binary
# with no libc dependency, which is what makes distroless static viable below.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/tether ./cmd/tether && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/tetherd ./cmd/tetherd

# Distroless has no shell to create this later, so it's created here instead.
RUN mkdir -p /home/nonroot/.tether && chown -R 65532:65532 /home/nonroot

# ---- runtime ---------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev

LABEL org.opencontainers.image.title="tether" \
      org.opencontainers.image.description="A local message bus for coding agents: a daemon and CLI that let agents in different harnesses register, message each other and acknowledge work over a unix socket." \
      org.opencontainers.image.source="https://github.com/praneethravuri/tether" \
      org.opencontainers.image.url="https://github.com/praneethravuri/tether" \
      org.opencontainers.image.documentation="https://github.com/praneethravuri/tether/blob/main/README.md" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

COPY --from=builder /out/tether  /usr/local/bin/tether
COPY --from=builder /out/tetherd /usr/local/bin/tetherd
COPY --from=builder --chown=65532:65532 /home/nonroot /home/nonroot

USER 65532:65532
ENV HOME=/home/nonroot

# Mount a host directory here to share the socket and database with agents
# running outside the container.
VOLUME ["/home/nonroot/.tether"]

ENTRYPOINT ["/usr/local/bin/tetherd"]
