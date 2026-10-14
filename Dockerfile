# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG GOPROXY=https://proxy.golang.org,direct

ENV GOPROXY=${GOPROXY}

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    set -eu; \
    for attempt in 1 2 3 4 5; do \
        if go mod download; then \
            exit 0; \
        fi; \
        if [ "$attempt" -eq 5 ]; then \
            exit 1; \
        fi; \
        sleep $((attempt * 2)); \
    done

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/project-name .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S app \
    && adduser -u 65532 -S -D -H -G app app \
    && mkdir -p /app/data \
    && chown -R app:app /app

WORKDIR /app

COPY --from=build /out/project-name /usr/local/bin/project-name
COPY --chown=app:app words /app/words

ENV LISTENING_PORT=8080 \
    WORDS_DIR=/app/words

EXPOSE 8080

USER 65532:65532

HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${LISTENING_PORT}/health/ready" || exit 1

ENTRYPOINT ["/usr/local/bin/project-name"]
