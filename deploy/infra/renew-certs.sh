#!/usr/bin/env bash
# 签发或续期 api、admin 与主域名（法律文本页）的 Let's Encrypt 证书。
#   首次（网关未启动、80 端口空闲）：bash renew-certs.sh issue
#   日常续期（网关运行中，走 webroot）：bash renew-certs.sh   —— 由 systemd 定时器每天执行
# img/vod（cdn.zikodrama-tv.com）证书走 DNS 手动验证并上传到 BytePlus，不在这里自动续期，到期前约 30 天需重新签发。
set -Eeuo pipefail
cd "$(dirname "$0")"
mkdir -p acme
if [ "${1:-renew}" = issue ]; then
  docker run --rm -p 80:80 -v /etc/letsencrypt:/etc/letsencrypt certbot/certbot:v3.0.1 \
    certonly --standalone --non-interactive --agree-tos --register-unsafely-without-email \
    --cert-name api.zikodrama-tv.com -d api.zikodrama-tv.com -d admin.zikodrama-tv.com
else
  # 网关 80 端口提供 webroot 校验：api/admin 一张，主域名法律文本页一张。
  for cert in api.zikodrama-tv.com zikodrama-tv.com; do
    docker run --rm -v /etc/letsencrypt:/etc/letsencrypt -v "$PWD/acme:/var/www/acme" certbot/certbot:v3.0.1 \
      renew --non-interactive --cert-name "$cert" --webroot -w /var/www/acme
  done
  docker exec tkdrama-gateway nginx -s reload
fi
