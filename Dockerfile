# ---- builder ----
FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/tether ./cmd/tether

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/tether /usr/local/bin/

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/tether"]
