# syntax=docker/dockerfile:1.7
FROM golang:1.26.5 AS builder

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} \
    GOWORK=off

WORKDIR /src

# Styx 源码随仓库放在 third_party/styx，go.mod 用 replace 指向它，构建不访问任何私有仓库。
COPY go.mod go.sum ./
COPY third_party/styx ./third_party/styx
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 与本地 Makefile 共用构建入口。
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 make build

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app

COPY --from=builder /src/bin/drama-server /app/server
WORKDIR /app
USER app

EXPOSE 18080
VOLUME /data/conf
CMD ["./server", "-conf", "/data/conf/config.yaml"]
