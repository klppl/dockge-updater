FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web

ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/dockge-updater \
    ./cmd/dockge-updater

FROM alpine:3.23

RUN apk add --no-cache \
    ca-certificates \
    docker-cli \
    docker-cli-compose \
    tzdata

COPY --from=build /out/dockge-updater /usr/local/bin/dockge-updater

ENV LISTEN_ADDR=:8080 \
    STACKS_DIR=/opt/stacks \
    DATA_DIR=/data

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/dockge-updater"]
