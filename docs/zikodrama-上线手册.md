# ZikoDrama 上线手册

主体：SAILSON PTE LTD（新加坡）。主域名：zikodrama-tv.com。播放通道：直连 BytePlus VePlayer。新建空库，不迁移客户旧数据。

## 一、域名规划

| 子域名 | 用途 | 解析到 |
| --- | --- | --- |
| api.zikodrama-tv.com | Mini 接口 `/api/`、BytePlus 回调 `/callbacks/` | 服务器公网 IP（A 记录） |
| admin.zikodrama-tv.com | 运营后台（同源 `/admin/` 接口） | 服务器公网 IP（A 记录） |
| vod.zikodrama-tv.com | 视频播放 CDN | BytePlus VOD 给的 CNAME |
| img.zikodrama-tv.com | 封面图片 CDN | BytePlus VOD 给的 CNAME |

证书：在服务器用 Let's Encrypt 签发 api、admin 双域名证书（`deploy/infra/renew-certs.sh issue`），systemd 定时器每天执行 `renew-certs.sh` 自动续期。vod、img 的证书由 BytePlus 管理。

## 二、要买或开通的东西

1. **服务器**：阿里云美国（硅谷），2 核 8G，ESSD 80G，按流量计费峰值 100Mbps，Alibaba Cloud Linux 4，密钥对 ziko-drama。装 Docker（含 compose 插件）和 Git。安全组只开 80、443，22 只对自己出口 IP 开放。
2. **域名**：zikodrama-tv.com 和通配符证书。
3. **BytePlus 新账号**（新加坡主体实名）：
   - 开通 VOD，地域选 `ap-singapore-1`（新加坡）。代码只接受这个或 `ap-southeast-1`（柔佛）。
   - 建一个空间，名称 3～40 位小写字母、数字、横线，例如 `tkdrama-sg`。
   - 给空间绑定 vod.zikodrama-tv.com（播放）和 img.zikodrama-tv.com（图片），上传证书，开 HTTPS。
   - 建一个只有 VOD 权限的 IAM 子用户，拿 AccessKeyID 和 SecretAccessKey。
   - 记下 AccountID 和 VOD AppID。
   - 事件回调地址填 `https://api.zikodrama-tv.com/callbacks/v1/byteplus/vod`，生成回调鉴权密钥。
   - **申请 VePlayer Web 基础版 License**：四个授权域名分别是 `tiktok-minis.us`、`tiktokminis.us`、`tiktok-minis.eu`、`tiktok-minis.com`，每个域名一份 `.lic` 文件。客户包里的四份 License 属于他原来的 BytePlus 账号，不能继续用。
4. **TikTok 开发者平台**（新加坡主体账号）：每个短剧应用新建 Mini，拿 AppID、Client Key、Client Secret；开通激励广告并拿广告位 ID。
5. **代码仓库**：在 GitHub 建三个私有仓库 drama-server、drama-admin、drama-mini，把这个工作区的三个目录推上去，默认分支用 `main`（部署脚本的 prod 模式拉 `main`）。

## 三、服务器部署

### 1. 基础设施

```bash
git clone <drama-server 仓库> ~/tkdrama/drama-server
git clone <drama-admin 仓库> ~/tkdrama/drama-admin
# 基础设施配置在 drama-server 仓库的 deploy/infra 下
cd ~/tkdrama/drama-server/deploy/infra
cp .env.example .env    # 填三个密码，各用 openssl rand -hex 24 生成
bash renew-certs.sh issue   # 首次签发证书（网关启动前，80 端口空闲时执行）
docker compose up -d mysql redis
```

### 2. 初始化数据库

先在本地生成第一个管理员的密码摘要（密码至少 12 位）：

```bash
python -c "import bcrypt,getpass;print(bcrypt.hashpw(getpass.getpass().encode(),bcrypt.gensalt(12)).decode())"
```

然后进 MySQL：

```bash
docker exec -it tkdrama-mysql mysql -uroot -p tk_drama
```

```sql
SET @drama_admin_username = 'admin';
SET @drama_admin_password_hash = '<上面生成的 60 位摘要>';
SET @drama_admin_nickname = '超级管理员';
SOURCE /drama-sql/init.sql;
-- 按文件名升序逐个执行 migrations 下全部脚本，每个只执行一次
SOURCE /drama-sql/migrations/20260930_001_drama_operators.sql;
-- ……一直到最后一个文件
```

在服务器上列出完整顺序：`ls ~/tkdrama/drama-server/sql/migrations | sort`。任何一步报错就停下排查，DDL 不能整体回滚，部分执行过的库不能直接重跑。

### 3. API 服务

编辑 `drama-server/configs/config.prod.yaml`：

- 两处 MySQL 的 `CHANGE_ME_MYSQL_PASSWORD` 换成 `.env` 里的 `MYSQL_DRAMA_PASSWORD`。
- Redis 的 `CHANGE_ME_REDIS_PASSWORD` 换成 `REDIS_PASSWORD`。
- `identity_token_encryption_secret`、`admin_jwt_signing_secret` 各填一个 `openssl rand -hex 32`，两个不能相同。

这个文件只在服务器上改，不要把真实密码提交回仓库。改完 `chmod 600`。

```bash
cd ~/tkdrama/drama-server && bash deploy.sh prod
curl -f http://127.0.0.1:18080/api/v1/health
```

### 4. 后台

```bash
cd ~/tkdrama/drama-admin && bash deploy.sh prod    # 监听本机 5175
```

### 5. 网关

```bash
cd ~/tkdrama/drama-server/deploy/infra && docker compose up -d gateway
curl -f https://api.zikodrama-tv.com/api/v1/health
```

浏览器打开 https://admin.zikodrama-tv.com，用第 2 步建的管理员登录。

## 四、后台配置

1. **系统管理 → 配置管理 → media**：填 BytePlus 的 AccountID、AK/SK、VOD AppID、地域 `ap-singapore-1`、空间名、封面域名 `https://img.zikodrama-tv.com`、回调地址和回调密钥。
2. **主体管理**：新建「SAILSON PTE LTD」主体。走直连通道可以不填 TikTok 发布方凭据。
3. **应用管理**：每个 Mini 录入 AppID、名称、Client Key、Client Secret、广告位，绑定上面的主体；播放通道选直连（`byteplus_web`）。
4. 上传一部测试剧，确认转码完成、封面能显示，在直连发布里上架。

## 五、Mini 打包

1. 把新申请的四份 `.lic` 放进 `drama-mini/src/assets/veplayer/`，删掉旧的四份，同步改 `src/config/veplayer-licenses.json` 里的文件名和 SDK AppID。
2. 每个应用在 `drama-mini/configs/apps/` 下建一个 `<AppID>.json`，照 `example.json` 填 AppID、名称、Logo、Client Key。
3. 每个应用在 TikTok 开发者平台 Security 里登记请求域名：
   `https://api.zikodrama-tv.com`、`https://vod.zikodrama-tv.com`、`https://img.zikodrama-tv.com`、`https://sf16-vod-license-multi.byteplusvod.com`、`https://sf19-vod-license-multi.byteplusvod.com`。
4. 先打 sandbox 包：`make package-sandbox APP=<AppID>`，上传后扫预览码，验证登录、目录、播放、激励广告解锁。
5. 通过后打正式包：`make package-production APP=<AppID>`，提交审核。
6. 隐私政策和用户协议在 Mini 内置页面，主体已改为 SAILSON PTE LTD，联系邮箱写的是 support@zikodrama-tv.com，这个邮箱需要开通（可以用域名邮件转发）。

## 六、相对客户原始代码的改动

| 位置 | 改动 |
| --- | --- |
| server `go.mod`、`third_party/styx` | 内置客户给的 Styx 源码，用 replace 指向本地，不再依赖 Gitee 私有仓库 |
| server `Dockerfile`、`deploy.sh` | 去掉构建时的 SSH 私钥和 known_hosts |
| server `scripts/setup-server.sh` 及其测试 | 删除（它从客户的 Gitee 仓库克隆） |
| server `scripts/deploy_test.go` | 测试改为校验构建不再使用 SSH |
| server `campaign_cover.go` | 投放封面只认 `img.zikodrama-tv.com` |
| server `configs/config.prod.yaml` | 改为同机 MySQL/Redis 的占位模板，库名 `tk_drama` |
| admin `.env.production`、`.env.staging` | API 指向 `https://admin.zikodrama-tv.com` |
| mini `src/config/mediaNetwork.ts` | 视频 CDN 改为 `vod.zikodrama-tv.com` |
| mini `src/runtime/launchRoute.ts`、`minis.config.json` | 图片域名改为 `img.zikodrama-tv.com` |
| mini `configs/apps/` | 删除客户的 25 个应用，只留示例 |
| mini `.env.development` | 清空客户的 AppID 和 Client Key |
| mini `src/pages/LegalPage.tsx` | 法律主体改为 SAILSON PTE LTD，邮箱改为 support@zikodrama-tv.com |
| 各项目测试文件 | 跟随域名和主体名称同步修改 |
| 新增 `deploy/infra/` | MySQL、Redis、网关的 docker compose；网关负责 HTTPS、跨域和 `/admin/` 转发，替代客户没给的 drama-nginx 工程 |

还没换的：VePlayer License（等新账号申请下来）。
