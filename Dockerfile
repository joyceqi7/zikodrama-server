# syntax=docker/dockerfile:1.7
FROM golang:1.26.5 AS builder

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} \
    GOPRIVATE=gitee.com/linpbin/* \
    GONOSUMDB=gitee.com/linpbin/* \
    GOWORK=off

# Go 仍使用原模块路径，Git 通过 BuildKit SSH 挂载读取 Gitee 私有仓库。
RUN mkdir -p -m 0700 /root/.ssh \
    && git config --global url."ssh://git@gitee.com/".insteadOf "https://gitee.com/"
ENV GIT_SSH_COMMAND="ssh -o BatchMode=yes -o StrictHostKeyChecking=yes"

WORKDIR /src

# 先下载依赖，源码修改时复用模块缓存。
COPY go.mod go.sum ./
RUN --mount=type=ssh,required=true \
    --mount=type=secret,id=gitee_known_hosts,target=/root/.ssh/known_hosts,mode=0444,required=true \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 与本地 Makefile 共用构建入口。
COPY . .
RUN --mount=type=ssh,required=true \
    --mount=type=secret,id=gitee_known_hosts,target=/root/.ssh/known_hosts,mode=0444,required=true \
    --mount=type=cache,target=/go/pkg/mod \
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
