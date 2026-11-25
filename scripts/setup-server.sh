#!/usr/bin/env bash
set -Eeuo pipefail

# 运维在新机器执行一次；Docker、Git 和有仓库读取权限的无口令私钥由运维准备。
# 用法：bash setup-server.sh [test|prod] [私钥路径] [项目目录]
deploy_environment="${1:-test}"
ssh_key_file="${2:-$HOME/.ssh/drama_gitee}"
project_dir="${3:-$HOME/services/drama-server}"
case "$deploy_environment" in
  test) branch=test ;;
  prod) branch=main ;;
  *) echo "用法：$0 [test|prod] [私钥路径] [项目目录]" >&2; exit 1 ;;
esac
if [ "$#" -gt 3 ]; then echo "参数过多" >&2; exit 1; fi

# 将运维提供的相对路径固定为绝对路径，后续登录和切换目录不会改变含义。
ssh_key_file="$(cd -- "$(dirname -- "$ssh_key_file")" && pwd)/$(basename -- "$ssh_key_file")"
case "$project_dir" in /*) ;; *) project_dir="$PWD/$project_dir" ;; esac
config_dir="${DRAMA_SETUP_CONFIG_DIR:-/etc/drama-server}"
profile_file="${DRAMA_SETUP_PROFILE_FILE:-/etc/profile.d/drama-server.sh}"
known_hosts_file="$config_dir/known_hosts"
repository=git@gitee.com:tiptops/drama-server.git

# 专用环境文件只有路径，没有私钥内容；已有不同内容时保留原文件并退出。
profile_contents="$(printf '# Drama deployment environment\nexport DEPLOY_SSH_KEY=%q\nexport DEPLOY_KNOWN_HOSTS_FILE=%q\n' "$ssh_key_file" "$known_hosts_file")"
if [ -e "$profile_file" ] && [ "$(cat -- "$profile_file")" != "$profile_contents" ]; then
  echo "已有环境配置，未作修改：$profile_file" >&2
  exit 1
fi

# 已有项目只复用，不改 origin、Git SSH 设置或业务配置。
new_checkout=false
if [ -e "$project_dir" ]; then
  remote_url="$(git -C "$project_dir" remote get-url origin)"
  case "$remote_url" in
    git@gitee.com:tiptops/drama-server.git|git@gitee-drama:tiptops/drama-server.git|gitee-drama:tiptops/drama-server.git|https://gitee.com/tiptops/drama-server.git) ;;
    *) echo "目标目录不是预期仓库，未作修改：$project_dir" >&2; exit 1 ;;
  esac
else
  new_checkout=true
fi

# 主机记录单独存放，不改 ~/.ssh/config 或 ~/.ssh/known_hosts。
# 首次连接记录主机公钥；已记录的主机公钥发生变化时，OpenSSH 会拒绝连接。
umask 077
mkdir -p -- "$config_dir"
printf -v git_ssh_command 'ssh -i %q -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=%q' "$ssh_key_file" "$known_hosts_file"
echo ">>> 连接 Gitee 私有依赖并准备专用主机记录..."
GIT_SSH_COMMAND="$git_ssh_command" git ls-remote --exit-code ssh://git@gitee.com/linpbin/styx.git HEAD >/dev/null

if [ "$new_checkout" = true ]; then
  mkdir -p -- "$(dirname -- "$project_dir")"
  GIT_SSH_COMMAND="$git_ssh_command" git clone --branch "$branch" "$repository" "$project_dir"
  # 仅为刚克隆的新仓库保存 SSH 命令，不修改系统或其他仓库的 Git 配置。
  git -C "$project_dir" config core.sshCommand "$git_ssh_command"
fi

mkdir -p -- "$(dirname -- "$profile_file")"
if [ ! -e "$profile_file" ]; then
  # noclobber 防止并发初始化意外覆盖已有配置。
  (set -o noclobber; printf '%s\n' "$profile_contents" > "$profile_file")
  chmod 644 "$profile_file"
fi
export DEPLOY_SSH_KEY="$ssh_key_file"
export DEPLOY_KNOWN_HOSTS_FILE="$known_hosts_file"

echo ">>> 执行首次部署..."
bash "$project_dir/deploy.sh" "$deploy_environment"
printf '\n初始化和首次部署完成。\n当前终端继续部署前执行一次：\n  source %q\n' "$profile_file"
printf '以后重新登录 Bash 后直接执行：\n  cd %q && bash deploy.sh %s\n' "$project_dir" "$deploy_environment"
