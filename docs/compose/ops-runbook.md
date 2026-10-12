# 运维手册（ops-runbook）

中转站日常运维速查。脚本均在 `D:\MIX4刷机\`，服务目录 `D:\MIX4刷机\workbuddy2api-panel\`。

## 日常

| 动作 | 命令 |
|---|---|
| 看指标 | 双击 `stats.html`（看门狗每 30 分钟自动刷新） |
| 查当前公网地址 | 记事本打开 `public_url.txt`（隧道重启自动更新） |
| 服务健康 | 浏览器 `http://localhost:8788/panel/`（密码 test_key） |
| 配置体检 | `powershell -File check-config.ps1` |
| 手动备份 | `powershell -File backup.ps1`（计划任务每日 03:00 自动跑） |
| 恢复备份 | `powershell -File restore.ps1 -WhatIf` 先校验，再去掉 -WhatIf 执行 |

## 重启与升级

| 动作 | 命令 |
|---|---|
| 优雅重启服务 | 看门狗自动（drain → 等 20s 收尾 → 拉起）；手动：`POST /debug/drain`（Bearer api_key）后启动 exe |
| 替换新 exe | 停服 → 覆盖 `wb2api.exe` → `powershell -File watchdog_all.ps1 -UpdateHash`（放行新哈希）→ 启动 |
| 日志归档 | `powershell -File watchdog_all.ps1 -ArchiveNow`（重启前也会自动归档到 `logs\archive\`） |

## 看门狗守护范围

- 双探测（15s）：本地 `:8788/panel/`+`/v1/models`，公网 `public_url.txt` 的 `/panel/`
- 进程缺失立即拉起；HTTP 连续 2 次失败判挂死拉起
- 重启前置闸门：**exe 哈希钉扎**（`exe.sha256`）→ **config 体检**（check-config.ps1）→ drain → 归档日志
- 隧道：QUIC + `--edge-ip-version 4`；重启后自动提取新 URL 写 `public_url.txt`

## 故障速查

| 症状 | 处理 |
|---|---|
| 手机连不上 | 看 `public_url.txt` 是否变了 → 换新 URL；确认电脑开机 |
| 公网 530/502 | 等看门狗 15s 自愈；超过 1 分钟手动 `Stop-Process cloudflared` 后等它拉起 |
| exe 校验失败拒启 | 确认二进制来源 → `watchdog_all.ps1 -UpdateHash` |
| config 校验失败 | `check-config.ps1` 看 FAIL 项 → 修 config.json（注意去 BOM） |
| 恢复后账号异常 | `restore.ps1 -From <日期>` 回滚到更早备份 |

## 安全说明

- `auths\` 与 `config.json` ACL 仅 Administrator+SYSTEM（收紧自 2026-09-28）
- **auths 落盘加密**（AES-256-GCM）：密钥在 `workbuddy2api-panel\crypto.secret`（自动生成，勿删勿泄）；读写透明——明文存量照常加载，首次 SaveAtomic 后转密文。外部脚本直接读 auths 会看到密文；需明文时在 config 设 `storage.encrypt: false` 重启
- `exe.sha256` 钉扎防二进制替换；换 exe 必须 `-UpdateHash`
- API Key 当前 `test_key`（方便优先）；公网仅应暴露 `/v1`，面板路径自行知悉
