# 云上热备部署（wb2api Docker）

用途：电脑关机时公网不中断——云主机跑同款中转站做热备，LSPilot 切 URL 即可继续。

## 前置

- 一台有公网的 Linux 主机（Docker + docker compose v2）
- 本仓库源码（或 `git clone https://github.com/linguo2625469/workbuddy2api-panel`）

## 步骤

```bash
git clone https://github.com/linguo2625469/workbuddy2api-panel.git
cd workbuddy2api-panel

# 1) 配置：listen 对齐 8788，api_key 自定（建议强随机，勿用 test_key）
cp config.example.json config.json
#    编辑 config.json: "listen": ":8788"

# 2) 账号池：从主机同步 auths\（每日备份的 backups\yyyyMMdd\auths 可直接用）
mkdir -p auths data
#    把 *.json 放进 ./auths/

# 3) 启动（PUID/PGID 用你自己的 uid，避免 auths 写权限问题）
PUID=$(id -u) PGID=$(id -g) docker compose up -d

# 4) 验证
curl -s http://127.0.0.1:8788/healthz
curl -s -H "Authorization: Bearer <你的api_key>" http://127.0.0.1:8788/v1/models
```

## 与生产的差异

| 项 | 本地主机 | 云热备 |
|---|---|---|
| 启动 | wb2api.exe 直跑 + watchdog_all | docker compose（restart: unless-stopped） |
| 隧道 | cloudflared 快速/named tunnel | 云主机直接暴露 8788 或同样挂 tunnel |
| 配置 | D:\MIX4刷机\workbuddy2api-panel\config.json | ./config.json 挂载 |
| 账号同步 | 原始 auths\ | 从 backups 同步，注意 token 时效 |

## 切换流程

1. 本地不可用时，LSPilot 的 API 地址改为 `http://<云主机IP>:8788/v1`（或云上的固定域名）
2. Key 用云上 config.json 里配置的 api_key
3. 本地恢复后切回；auths 以较新一侧为准（token 刷新后注意同步）

## 健康检查与日志

- healthcheck 探测 `/healthz`（30s 间隔，3 次失败重启）
- 日志轮转：json-file 10m × 3
- 数据卷：`./auths`、`./data`、`./config.json` 挂载，容器重建不丢
