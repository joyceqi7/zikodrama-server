#!/usr/bin/env bash
set -Eeuo pipefail

# 先完整读取函数，再更新仓库，避免 Bash 在执行中读取被改写的脚本片段。
deploy_main() {
  local deploy_environment="${1:-test}" deploy_branch project_root
  local -a network_args
  case "$deploy_environment" in
    test) deploy_branch=test; network_args=(--network bridge -p 18080:18080) ;;
    prod) deploy_branch=main; network_args=(--network host) ;;
    *) echo "用法: $0 [test|prod]" >&2; return 1 ;;
  esac
  if [ "$#" -gt 1 ]; then echo "用法: $0 [test|prod]" >&2; return 1; fi
  project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
  cd "$project_root"

  if [ "${DEPLOY_REEXEC:-}" != 1 ]; then
    echo "=== 部署 drama-server [$deploy_environment] ==="
    echo ">>> 拉取 $deploy_branch 分支..."
    git fetch origin "$deploy_branch"
    git checkout "$deploy_branch"
    git pull --ff-only origin "$deploy_branch"

    # 重新读取拉取后的脚本；标记仅用于跳过第二次拉取，避免无限重启。
    DEPLOY_REEXEC=1 exec bash "$project_root/deploy.sh" "$deploy_environment"
  fi
  unset DEPLOY_REEXEC

  local app_name=drama-server
  local config_file="$project_root/configs/config.$deploy_environment.yaml"
  # SSH 环境由运维配置，直接交给 Docker 使用，由构建命令返回实际错误。
  local ssh_key_file="${DEPLOY_SSH_KEY:-$HOME/.ssh/id_rsa}"
  local known_hosts_file="${DEPLOY_KNOWN_HOSTS_FILE:-$HOME/.ssh/known_hosts}"
  local version image_tag
  version="$(git rev-parse --short HEAD)"
  image_tag="$app_name:$deploy_environment-$version"
  echo ">>> 镜像: $image_tag"
  echo ">>> 构建镜像..."
  # 显式传入文件类型，保证与 Dockerfile 所需的 secret 名称一致。
  DOCKER_BUILDKIT=1 docker build \
    --ssh "default=$ssh_key_file" \
    --secret "type=file,id=gitee_known_hosts,src=$known_hosts_file" \
    -t "$image_tag" \
    -t "$app_name:$deploy_environment-latest" \
    .

  # 先让镜像内的 app 用户拥有挂载目录和已有日志文件，再替换旧容器。
  docker run --rm --network none --user root \
    -v "$project_root/logs:/app/logs" \
    --entrypoint sh "$image_tag" -c 'chown -R app /app/logs && chmod -R u+rwX /app/logs'

  echo ">>> 重启容器..."
  docker rm -f "$app_name" 2>/dev/null || true
  docker run -d \
    --name "$app_name" \
    --restart always \
    --log-driver json-file \
    --log-opt max-size=200m \
    --log-opt max-file=7 \
    "${network_args[@]}" \
    -v "$project_root/logs:/app/logs" \
    -v "$config_file:/data/conf/config.yaml:ro" \
    "$image_tag"

  sleep 2
  if [ "$(docker inspect --format '{{.State.Running}}' "$app_name")" != true ]; then
    echo "=== 部署失败! ===" >&2
    docker logs --tail 50 "$app_name"
    return 1
  fi

  echo "=== 部署成功! ==="
  echo ">>> 环境: $deploy_environment"
  echo ">>> 版本: $version"
  echo ">>> 日志目录: $project_root/logs"
  echo ">>> 公网域名和 TLS 由 drama-nginx 独立管理，本次仅更新 API 容器。"
  # 只清理当前环境的历史版本；按镜像创建时间保留最近 5 个不同镜像。
  docker image ls "$app_name" --format '{{.Tag}} {{.ID}}' \
    | awk -v prefix="$deploy_environment-" 'index($1, prefix) == 1 && $1 != prefix "latest" { if (!($2 in rank)) rank[$2] = ++count; if (rank[$2] > 5) print $1 }' \
    | while IFS= read -r image_tag; do docker image rm "$app_name:$image_tag" 2>/dev/null || true; done
  echo ">>> 最近日志:"
  docker logs --tail 10 "$app_name"
}

deploy_main "$@"
