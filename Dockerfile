# syntax=docker/dockerfile:1.7

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/loader ./cmd/loader

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/loader /loader
USER nonroot:nonroot
EXPOSE 9090
# Distroless has no shell; k8s/systemd probes hit /healthz instead of Docker HEALTHCHECK.
ENTRYPOINT ["/loader"]
