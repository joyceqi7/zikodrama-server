#!/usr/bin/env bash
# ZikoShorts：同机第二套独立实例（独立库 tk_drama_zs、Redis db 1、独立 BytePlus 账号），
# 和 ZikoDrama 共用代码、MySQL/Redis 容器和网关，只换配置与端口：API 18081、后台 5176。
#
# 用法（在服务器上）：
#   bash deploy.sh server   # 复用 ZikoDrama 已构建的 drama-server:prod-latest 镜像，重启 drama-server-zikoshorts
#   bash deploy.sh admin    # 用 admin.zikoshorts.com 构建后台镜像，重启 drama-admin-zikoshorts
#
# 实例目录 /root/tkdrama/zikoshorts 存放 config.yaml（含密码与密钥，不进仓库）和 logs/。
# 先发布 ZikoDrama 的 drama-server（./deploy.sh prod），这里再重启即可拿到同一版本代码。
set -Eeuo pipefail

INSTANCE_DIR=/root/tkdrama/zikoshorts
ADMIN_REPO=/root/tkdrama/drama-admin

# 后端容器沿用 ZikoDrama 生产镜像，仅挂载 ZikoShorts 自己的配置和日志目录。
deploy_server() {
  local name=drama-server-zikoshorts image=drama-server:prod-latest
  [ -f "$INSTANCE_DIR/config.yaml" ] || { echo "缺少 $INSTANCE_DIR/config.yaml" >&2; return 1; }
  mkdir -p "$INSTANCE_DIR/logs"
  # 镜像内以 app 用户运行，先把日志目录交给它。
  docker run --rm --network none --user root -v "$INSTANCE_DIR/logs:/app/logs" \
    --entrypoint sh "$image" -c 'chown -R app /app/logs && chmod -R u+rwX /app/logs'
  docker rm -f "$name" 2>/dev/null || true
  docker run -d --name "$name" --restart always \
    --log-driver json-file --log-opt max-size=200m --log-opt max-file=7 \
    --network host \
    -v "$INSTANCE_DIR/logs:/app/logs" \
    -v "$INSTANCE_DIR/config.yaml:/data/conf/config.yaml:ro" \
    "$image"
  sleep 2
  if [ "$(docker inspect --format '{{.State.Running}}' "$name")" != true ]; then
    echo "drama-server-zikoshorts 启动失败" >&2
    docker logs --tail 50 "$name"
    return 1
  fi
  echo "已启动 $name（镜像 $(docker inspect --format '{{.Config.Image}}' "$name")）"
}

# 后台用同一份 drama-admin 代码，只把 API Origin 换成 admin.zikoshorts.com；只监听本机，由网关对外。
deploy_admin() {
  local name=drama-admin-zikoshorts commit version image
  cd "$ADMIN_REPO"
  commit=$(git rev-parse HEAD)
  version="$(git rev-parse --short HEAD)-$(date -u +%Y%m%d%H%M%S)"
  image="drama-admin:zikoshorts-$version"
  docker build -f Dockerfile \
    --build-arg VITE_API_BASE_URL=https://admin.zikoshorts.com \
    --build-arg "RELEASE_ID=$version" --build-arg "BUILD_COMMIT=$commit" \
    -t "$image" -t drama-admin:zikoshorts-latest .
  docker rm -f "$name" 2>/dev/null || true
  docker run -d --name "$name" --restart unless-stopped \
    --log-driver json-file --log-opt max-size=20m --log-opt max-file=3 \
    -p 127.0.0.1:5176:80 "$image"
  # 只保留最近 3 个 ZikoShorts 后台镜像。
  docker image ls drama-admin --format '{{.Tag}}' | grep '^zikoshorts-[0-9a-f]' | sort -r | tail -n +4 \
    | while IFS= read -r tag; do docker image rm "drama-admin:$tag" 2>/dev/null || true; done
  echo "已启动 $name（镜像 $image）"
}

case "${1:-}" in
  server) deploy_server ;;
  admin) deploy_admin ;;
  *) echo "用法: bash deploy.sh server|admin" >&2; exit 1 ;;
esac
