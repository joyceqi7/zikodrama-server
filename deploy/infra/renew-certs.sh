#!/usr/bin/env bash
# 签发或续期 api、admin 的 Let's Encrypt 证书。
#   首次（网关未启动、80 端口空闲）：bash renew-certs.sh issue
#   日常续期（网关运行中，走 webroot）：bash renew-certs.sh   —— 由 systemd 定时器每天执行
set -Eeuo pipefail
cd "$(dirname "$0")"
mkdir -p acme
if [ "${1:-renew}" = issue ]; then
  docker run --rm -p 80:80 -v /etc/letsencrypt:/etc/letsencrypt certbot/certbot:v3.0.1 \
    certonly --standalone --non-interactive --agree-tos --register-unsafely-without-email \
    --cert-name api.zikodrama-tv.com -d api.zikodrama-tv.com -d admin.zikodrama-tv.com
else
  docker run --rm -v /etc/letsencrypt:/etc/letsencrypt -v "$PWD/acme:/var/www/acme" certbot/certbot:v3.0.1 \
    renew --non-interactive --webroot -w /var/www/acme
  docker exec tkdrama-gateway nginx -s reload
fi
