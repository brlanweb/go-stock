# go-stock 标准发布流程

## 流程总览

```
本机开发 → 编译/单测通过 → git commit + push GitHub
                                    │
                  （Tabby MCP / SSH 触发服务器脚本）
                                    ▼
        服务器 /opt/go-stock/deploy/release.sh
        ├─ 1. git 拉取 GitHub（/opt/go-stock/src）
        ├─ 2. docker build → go-stock:<短commit>
        ├─ 3. 备份并切换 docker-compose.yml
        ├─ 4. 滚动更新 + 三重验证（health / ERROR 日志 / 风险感知冒烟）
        └─ 5. 验证失败自动回滚上一版本
```

## 发布命令

本机推送后，在服务器上执行（通过 Tabby MCP 的 `exec_command`，或直接 SSH）：

```bash
/opt/go-stock/deploy/release.sh                  # 发布 origin/main 最新提交
/opt/go-stock/deploy/release.sh v1.5.5           # 发布指定 tag/分支/commit
/opt/go-stock/deploy/release.sh main --skip-pull # 不拉远端，本地代码重建
```

低配服务器（2核4G）构建约 3-5 分钟，建议后台执行并轮询：

```bash
nohup /opt/go-stock/deploy/release.sh > /tmp/release.out 2>&1 &
tail -f /tmp/release.out
```

## 目录约定（服务器）

| 路径 | 说明 |
|---|---|
| `/opt/go-stock/docker-compose.yml` | 常驻编排文件，发布时仅替换 go-stock 的 image/build 两行 |
| `/opt/go-stock/.env` | 数据库/AI/Token 配置，发布永不触碰 |
| `/opt/go-stock/src` | 脚本管理的 git 克隆，勿手工改动 |
| `/opt/go-stock/releases/logs/` | 构建与发布日志（保留最近 5 份） |
| `docker-compose.yml.bak-*` | 发布前自动备份（保留最近 5 份），回滚依据 |

## 手动回滚

脚本失败时已自动回滚。如需人工回退到任意历史版本：

```bash
# 方式一：直接发布旧提交
/opt/go-stock/deploy/release.sh <旧commit>

# 方式二：恢复 compose 备份（镜像还在本地时秒级）
cp /opt/go-stock/docker-compose.yml.bak-<时间>-pre-<commit> /opt/go-stock/docker-compose.yml
cd /opt/go-stock && docker compose up -d --force-recreate go-stock
```

## 安全边界

- 脚本有并发锁（`/tmp/go-stock-release.lock`），重复触发会直接拒绝
- 目标提交与运行版本一致时幂等跳过，不做无意义重建
- 构建失败不触碰 compose，线上无感知
- 磁盘余量 < 5G 时拒绝发布
- 迁移由应用启动时自动执行；启动日志出现 `level=ERROR` 视为发布失败并回滚
