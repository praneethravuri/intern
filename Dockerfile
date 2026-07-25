# Ships the compiled tetherd + tether binaries only -- no shell, no claude/npx/node.
# This image is a service/embedding artifact: run tetherd with its socket directory
# volume-mounted so a host-side `tether` CLI can reach it, or copy the binaries out of
# this image into your own. `tether run claude` will not work inside this container as
# shipped, since the commands it spawns (claude, npx, a shell) aren't installed here.

# ---- builder ----
FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/tetherd ./cmd/tetherd
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/tether ./cmd/tether

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/tetherd /out/tether /usr/local/bin/

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/tetherd"]
