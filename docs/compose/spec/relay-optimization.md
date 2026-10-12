---
feature: relay-optimization
status: delivered
updated: 2026-09-28
branch: feat/relay-optimization
commits: 1e23c2b..8f7f413
---

# 中转站全方面优化（实用性 / 稳定性 / 响应速度）


## Report

**What was built** — 中转站全方面优化（除账号自动摘除）交付完毕。T0 把实现基线从旧快照切到上游 v1.11.9 并重编译升级生产 wb2api.exe（白拿池最早到期优先路由、SSE 修复、面板增强）。T1–T2 为看门狗加 QUIC 减振（--edge-ip-version 4）与重启前日志归档（logs\archive，保留 30 份）。T3 每日备份 auths+config（幂等、SHA256 manifest、03:00 计划任务）。T4 header_timeout 120→60 提速失败切换。T5 stats-gen 生成指标页（成功率/延迟/TTFB/账号 Top/模型分布）。T6–T7 为 Go 服务加 POST /debug/drain（与 SIGTERM 同路径、20s 停机窗）并接入看门狗重启流程（drain→等优雅退出→再启动）。T8 compose 补 healthcheck/日志轮转/端口 8788 对齐 + 云热备部署文档。

**Verification** — go build ./... exit=0；go test ./internal/server ./cmd/server ok（8.138s/0.079s）；e2e POST /debug/drain → 202 {"status":"draining"}→进程优雅退出（log: drain requested→bye）；T7 集成日志链 drain 应答 202→drain 完成，进程已优雅退出→wb2api 已启动 pid=7324；backup.ps1 双跑幂等（12 auths+manifest）+ 计划任务 Ready；stats.html q=7864 succ=94.35%；config header_timeout=60 后 panel/models 200；watchdog -Once 体检 exit=0。评审两轮：首轮 CRITICAL（T8 端口 7863 残留+compose 乱码）已修复（8f7f413），复审 PASS。

**Journey log**
- 旧快照≠新源码：wb2api-deploy 单提交旧快照，上游 linguo2625469/workbuddy2api-panel 才是真源；升级先于自研改动收益最大。
- Windows PS5.1 Get-Content -Raw 按 GBK 读 UTF-8 会吞换行：spec/compose 曾损坏，改用 [IO.File]::ReadAllText(..., UTF8) 或 Write 工具。
- exe 体积同值无法判版本：用 SHA256 + 二进制内 '/debug/drain' 标记；旧 exe 无端点时期 drain 404 是预期回退。
- 进程匹配自杀：CommandLine 匹配串含脚本名会命中工具壳自身，须锚定 -File 行尾并排除 $PID。
- Windows 下无法向隐藏进程发信号：HTTP /debug/drain 是看门狗触发优雅停机的等价入口。

## [S1] Problem

workbuddy2api 中转站当前可用，但离「实用、好用、方便、稳定、模型响应快」还有差距：

1. QUIC 隧道抖动（日志实证 `no recent network activity`），自愈有但缺减振手段；
2. 服务重启用 `-Force` 硬杀，在飞请求被掐断；日志随重启被 redirect 截断，排障证据丢失；
3. 无备份、无指标视图，账号用量/成功率/延迟要人肉翻日志；
4. `header_timeout=120s` 过长，上游挂死时请求长时间占住 in-flight 槽位；
5. 电脑关机公网全断，云热备仅有源码（Dockerfile/compose）未配置完善。

约束（用户决定）：账号自动摘除不做；`max_in_flight`/`session_sticky` 为用户自改值不动；面板入口保持现状（方便优先）；named tunnel 等 EU.org 过审另行切换。

## [S2] Design

### [S2.0] 上游同步（T0，先于一切实现）
上游源码项目 `github.com/linguo2625469/workbuddy2api-panel` 已到 **v1.11.9**（本地运行 exe 为 v1.11.6-panel；`wb2api-deploy` 为单提交旧快照）。实现基线直接切到 tag `v1.11.9`（`git reset --hard v1.11.9`，保留本 spec 未跟踪文件），随后用 Go 1.23.4 重新编译 `wb2api.exe` 并替换生产二进制（旧 exe 归档备份）。上游相对本地快照含：池「最早到期优先路由」、SSE 修复、面板积分到期分布/配色、成长任务每日排程、Dockerfile 配置挂载化与直启、defaultprompt.md 特性。

### [S2.1] 隧道减振
cloudflared 启动参数显式 `--protocol quic --edge-ip-version 4`（快速隧道模式），排除 IPv6 路径变数。`watchdog_all.ps1` 快速隧道与 token 两分支均携带 `--edge-ip-version 4`。

### [S2.2] 日志归档
`watchdog_all.ps1` 在执行任何进程重启（wb2api/cloudflared）之前，将 `server.log`、`server_err.log`、`tunnel_out.txt`、`tunnel_err.txt`、`watchdog_all.log` 复制到 `<BaseDir>\logs\archive\yyyyMMdd-HHmmss\` 并截断原文件。归档目录保留最近 30 份。提供 `-ArchiveNow` 手动归档开关。

### [S2.3] 每日备份
`backup.ps1`：把 `workbuddy2api-panel\auths\`（全部 json）与 `config.json` 复制到 `<BaseDir>\backups\yyyyMMdd\`，写 `manifest.txt`（文件清单+SHA256）；同一日期重复运行覆盖同日目录（幂等）；保留最近 30 天。`-RegisterTask` 注册每日 03:00 计划任务。

### [S2.4] 超时提速
`config.json` 的 `upstream.header_timeout_seconds` 120 → 60。总超时 `timeout_seconds=120` 与其余字段（含用户自改的 `max_in_flight`、`session_sticky`）不动。生效需重启 wb2api。

### [S2.5] 指标面板
`stats-gen.ps1` 读 `data\usage.json` 与 server.log（含归档）TTFB 采样，生成 `stats.html`（深色控制台风格）：总请求/成功率/平均延迟/TTFB p50 p95、token 用量、账号 Top10、模型分布。纯静态产物，双击即看。

### [S2.6] 优雅重启（drain）
Go 侧（基于 v1.11.9 既有 SIGTERM 优雅停机路径，扩展 HTTP 入口）：
- `server.Config.Drain func()` 回调；`POST /debug/drain`（Bearer api_key）应答 `202 {"status":"draining"}` 后异步触发，与 SIGTERM 同路径（`p.Flush` + `srv.Shutdown`）；
- 停机窗 5s → 20s（覆盖 hy4 长思考/长流式）；Shutdown 期间监听关闭、新连接被拒（等价 503 语义）；
- `watchdog_all.ps1` 重启 wb2api 前 POST `/debug/drain`（读 config.json api_key），等进程优雅退出（≤25s），仍未退出才 `Stop-Process -Force`。

### [S2.7] 云热备就绪
`docker-compose.yml` 补 healthcheck（`/healthz`）、日志轮转（json-file 10m×3）、端口对齐 8788（restart 与 volume 已有）。`docs/compose/deploy-remote.md` 部署步骤（本机无 Docker/云主机，交付配置与文档）。

## [S3] Out of Scope

- 账号自动摘除 / 健康度调度（用户明确排除）
- named tunnel 切换执行（等 EU.org 过审；`named-tunnel-setup.ps1` 已就绪）
- 面板收口、换 API Key（方便优先，保持 `test_key` 现状）
- `max_in_flight` / `session_sticky` / 池调度策略修改（用户自改值）
- Docker 镜像构建与云主机采购/部署执行（本机无 Docker；仅交付配置与文档）
- 论坛资料爬取（浏览器链路另案处理；本 feature 以本机实证为准）

## Tasks

- [x] T0: 工作基线切到上游 v1.11.9 并重编译升级生产 wb2api.exe（旧 exe 归档） — acceptance: 运行进程为 v1.11.9 构建，panel 200、/v1/models 200（covers: S2.0）
- [x] T1: watchdog_all 两个隧道分支启动参数加 `--edge-ip-version 4` — acceptance: 启动参数含该开关，cloudflared 正常注册（covers: S2.1）
- [x] T2: watchdog_all 重启前日志归档到 logs\archive\ 时间戳目录并截断原文件，保留 30 份 — acceptance: 触发重启后归档目录出现且内容为重启前日志（covers: S2.2）
- [x] T3: backup.ps1 备份 auths+config 到 backups\yyyyMMdd\ 附 manifest，-RegisterTask 注册每日 03:00 任务 — acceptance: 连续运行两次幂等，产物含全部 auths json 与 config.json（covers: S2.3）
- [x] T4: config.json header_timeout_seconds 改为 60 并重启 wb2api — acceptance: 配置值为 60，panel 200、/v1/models 200（covers: S2.4）
- [x] T5: stats-gen.ps1 生成 stats.html（成功率/延迟/TTFB/账号 Top/模型分布） — acceptance: 页面四类指标可见且数字与 usage.json 一致（covers: S2.5）
- [x] T6: Go 实现 /debug/drain + SIGTERM 同路径优雅退出 — acceptance: 调 /debug/drain 得 202 后进程优雅退出（在飞请求保留至 20s 窗口），退出码 0（covers: S2.6）
- [x] T7: watchdog_all 重启 wb2api 先 drain 再启动 — acceptance: 日志出现「drain 应答 202 → drain 完成，进程已优雅退出 → wb2api 已启动」（covers: S2.6; depends: T6）
- [x] T8: docker-compose 加 healthcheck/日志轮转/端口对齐，写 deploy-remote.md — acceptance: compose 含 healthcheck+logging，文档含完整部署步骤（covers: S2.7）
