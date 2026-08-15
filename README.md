# go-stock

> 低内存 A 股数据底座：Go 单二进制集成 REST API、MCP Streamable HTTP、Vue 3 市场云图和 MySQL 历史数据同步。

**当前稳定版本：v1.2.2**

[![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)](https://go.dev/) [![Vue](https://img.shields.io/badge/Vue-3-42B883?logo=vuedotjs)](https://vuejs.org/) [![MySQL](https://img.shields.io/badge/MySQL-5.7%2B-4479A1?logo=mysql)](https://www.mysql.com/)

## 设计边界

- **查询只读本地库**：页面、REST 查询端点和 MCP 分析工具只读取 MySQL；不会因东方财富等上游瞬断在浏览器或 AI 分析时返回 `EOF`。
- **外部数据仅后台采集**：交易日 `12:00`（上午收盘）和 `16:00`（全天收盘）按 `Asia/Shanghai` 定时采集全市场快照；手动同步和缺失补齐属于显式写操作。
- **快照可追溯**：报价和指数响应携带 `fetched_at`，代表最近一次本地快照的采集时间，不将缓存快照伪装成实时数据。
- **云图按专业板块分区**：行业固定使用东方财富 31 个一级行业；概念从真实概念成分中选取主要板块。板块及板块内个股均以流通市值分配面积，涨跌幅仅决定红绿色；每板块最多展示流通市值前 50 只证券。
- **流式个股 Agent**：详情页右侧抽屉支持流式回答、按证券保存 MySQL 对话历史，并可选择是否携带当前快照及 `0/10/30/60` 个交易日日 K；历史条数由后端校验和截取。
- **指标管理与确定性回测**：内置传统技术指标和参考策略目录，支持 7 个纯 K 线策略按 A 股 T+1、交易成本和下一交易日执行语义回测，并在详情图标注买卖点。

## 特性

- **本地市场快照**：交易日 `12:00` 保存上午快照、`16:00` 保存收盘快照；报价、指数、自选股、页面和 MCP 分析工具均读取最近一次 MySQL 快照
- **后台采集降级**：实时行情东方财富（全字段）→ 腾讯 → 新浪；历史K线东方财富 → BaoStock → AKShare → 腾讯，自动降级 + 熔断 + 限流抖动
- **行情维度**：价格/涨跌/开高低昨收/量额/量比/换手/振幅/PE动/PB/总市值/流通市值/52周高低/买卖五档/数据源与时间戳元数据
- **历史K线**：不复权原始数据 + 累积复权因子完整入库（前复权随时重算，除权无需重刷）；日线存储，周/月线 SQL 聚合
- **受控缺失补齐**：`sync_checkpoint` 表记录进度，历史为空或落后最近交易日时低速补齐；完整证券不重复请求上游。历史源按 `东方财富 → BaoStock → AKShare → 腾讯` 降级：BaoStock 覆盖沪深股票/ETF，AKShare（新浪源）覆盖 ETF 与北交所 `920` 代码，避免单一上游被限流时整批失败
- **定时快照**：按 `Asia/Shanghai` 在工作日 `12:00` 采集上午快照，`16:00` 采集收盘快照并写入日K/每日指标
- **每日收盘复盘闭环**：交易日 `17:00` 基于本地收盘指数、市场宽度、板块强弱、当日盘前热点兑现情况，以及最近 5 个推荐日的个股表现生成结构化复盘；推荐结果同时计算沪深300近似基准收益和超额收益，并逐条回验上次优化指令。历史运行可追溯，最多 5 条新指令自动注入次日 `08:10` 趋势推荐
- **MCP Streamable HTTP**（`/mcp`）：本地 MySQL 分析工具 + 显式单股同步工具，供 LobeHub / Claude 等 MCP 客户端调用
- **Vue3 前端**：全屏市场终端云图，一级行业和主要概念按流通市值映射面积、颜色按涨跌幅映射；个股日K/周K/月K 图、指标管理和策略回测
- **低内存**：进程常驻约 20~40MB；扩展预留 US/CRYPTO market 字段与 Provider 接口

## 快速开始

### 本机运行

```bash
cp .env.example .env   # 填写数据库密码
pip install -r python-provider/requirements.txt   # BaoStock/AKShare 历史降级源
cd web && npm install && npm run build && cd ..
go build -o bin/go-stock ./cmd/server
./bin/go-stock
# 打开 http://localhost:8480
```

### Docker 部署

```bash
cp .env.example .env   # 填写配置
docker compose up -d --build
docker stats go-stock  # 观察内存
```

## 环境变量（.env）

| 变量 | 默认 | 说明 |
|---|---|---|
| GOSTOCK_ADDR | :8480 | 监听地址 |
| GOSTOCK_DB_HOST/PORT/NAME/USER/PASSWORD | - | MySQL 连接（密码必填） |
| GOSTOCK_MCP_TOKEN | 空 | MCP Bearer 鉴权，公网部署必设 |
| GOSTOCK_ACCESS_PASSWORD | 空 | 页面访问密码；设置后前端与查询接口需登录，健康检查与 MCP 不受影响 |
| GOSTOCK_BACKFILL_WORKERS | 1 | 历史补齐并发数；固定出口建议保持 1 |
| GOSTOCK_BACKFILL_QPS | 0.35 | 单数据源历史补齐 QPS |
| GOSTOCK_SYNC_SECTORS | false | 启动历史补齐时是否同时刷新行业/概念成分 |
| GOSTOCK_PYTHON_COMMAND | python3 | BaoStock/AKShare 历史降级源使用的 Python 解释器 |
| GOSTOCK_PYTHON_KLINE_SCRIPT | python-provider/fetch_kline.py | 历史K线降级桥接脚本路径 |
| GOSTOCK_QUOTE_TTL | 3 | 交易时段行情缓存秒数 |
| GOSTOCK_WATCHLIST_SYNC_SECONDS | 5 | 自选股实时行情后台同步周期；完整批次原子写入 Redis |
| GOSTOCK_AI_BASE_URL | 空 | OpenAI 兼容 API 基础地址，如 `https://api.openai.com/v1` |
| GOSTOCK_AI_API_KEY | 空 | 模型 API Key，仅写入本地或服务器 `.env` |
| GOSTOCK_AI_MODEL | 空 | 模型正式标识，如服务商实际提供的模型 ID |
| GOSTOCK_AI_PROMPT_FILE | config/ai_prompt.md | AI 趋势推荐提示词文件路径（长提示词放文件，配置只填路径） |
| GOSTOCK_AI_PROMPT | 空 | 可选内联短提示词；留空则用提示词文件，再空则用内嵌默认 |
| GOSTOCK_AI_HOTSPOT_PROMPT_FILE | config/hotspot_prompt.md | 热点漏斗的产业链分析提示词文件 |
| GOSTOCK_AI_REVIEW_PROMPT_FILE | config/review_prompt.md | 每日 17:00 收盘复盘提示词文件；复盘指令自动注入次日推荐 |
| GOSTOCK_HOTSPOT_BLACKLIST_FILE | config/hotspot_blacklist.txt | 泛概念过滤关键词文件，每行一个关键词 |

### AI 推荐配置示例

程序会在 `GOSTOCK_AI_BASE_URL` 后自动拼接 `/chat/completions`。不要把 API Key 提交到 Git 仓库，也不要在聊天中发送密钥。

```env
GOSTOCK_AI_BASE_URL=https://api.openai.com/v1
GOSTOCK_AI_API_KEY=在服务器本地填写真实密钥
GOSTOCK_AI_MODEL=gpt-5.6-sol
GOSTOCK_AI_PROMPT=基于候选股最近60个交易日OHLCV，评估未来10个交易日维持上涨趋势的概率，只返回3只并说明原因。
```

修改 `.env` 后重启服务，再手动验证：

```bash
docker compose up -d --force-recreate go-stock
curl -X POST http://127.0.0.1:8480/api/v1/recommendations/run
curl http://127.0.0.1:8480/api/v1/recommendations
```

## REST API

| 端点 | 说明 |
|---|---|
| `GET /api/v1/quote/{code}` | 最近一次本地证券快照（600519 / SH600519 / 000001.SZ 均可） |
| `GET /api/v1/quotes?codes=600519,000001` | 最近一次本地批量快照（≤100只） |
| `GET /api/v1/kline/{code}?period=day&adjust=qfq&limit=250` | K线（day/week/month，qfq/none） |
| `GET /api/v1/intraday/{code}` | 当日一分钟 OHLCV 蜡烛；仅 Redis 缓存，下一工作日 09:00 过期 |
| `GET /api/v1/timeshare/{code}` | 当日一分钟分时价格与均价；不写入 MySQL |
| `GET /api/v1/search?q=茅台` | 仅搜索本地证券基础信息 |
| `GET /api/v1/indices` | 最近一次本地指数快照 |
| `GET /api/v1/security/{code}` | 基础信息 |
| `GET /api/v1/indicator/{code}` | 每日指标历史 |
| `GET /api/v1/market/heatmap?market=all&group_by=industry&metric=change_pct&period=1d&limit=31` | 本地日K与指标生成的一级行业/主要概念云图；板块和个股面积按流通市值，每板块最多 50 只 |
| `GET /api/v1/hotspot`、`POST /api/v1/hotspot/run` | 查询或触发热点漏斗；按数据初筛、关系收敛、AI产业链分析、本地回验输出有效概念。配置 AI 后交易日 08:00 自动运行盘前分析 |
| `GET /api/v1/review`、`GET /api/v1/review/history` | 查询最新或指定历史每日复盘；包含指数、市场宽度、板块、盘前热点回验、最近 5 个推荐日的基准/超额收益、上次指令回验、风控和次日优化指令 |
| `GET /api/v1/review/status`、`POST /api/v1/review/run` | 查询或手动触发每日复盘；配置 AI 后交易日 17:00 自动运行 |
| `GET/PUT /api/v1/indicators[/{id}]` | 指标与策略目录、启停及参数管理 |
| `POST /api/v1/indicators/{id}/reset` | 恢复指标默认参数 |
| `POST /api/v1/backtest` | 使用本地日 K 执行确定性 A 股策略回测 |
| `GET /api/v1/backtest/history/{code}` | 查询证券历史回测记录 |
| `POST /api/v1/agent/chat/stream` | 个股 Agent SSE 流式对话；`history_days` 仅支持 `0/10/30/60`，`include_stock` 控制是否携带个股快照 |
| `GET/DELETE /api/v1/agent/chat/history/{code}` | 查询或清除当前证券保存在 MySQL 的 Agent 对话历史 |
| `GET/POST/DELETE /api/v1/watchlist[/{code}]` | 自选股与本地快照 |
| `GET /api/v1/sync/status` | 回填进度 |
| `POST /api/v1/sync/stock/{code}?mode=latest\|missing\|full` | 显式同步单只证券 |
| `POST /api/v1/sync/backfill` | 启动受控的全市场缺失历史补齐（完整证券跳过上游） |
| `POST /api/v1/sync/backfill/stop` | 停止历史补齐并保留断点 |
| `POST /api/v1/sync/backfill/retry-failed` | 用户显式重排失败项，避免服务重启后无限自动重试 |
| `POST /api/v1/sync/daily` | 手动触发本地市场快照采集 |

## MCP 使用教程

go-stock 在 `/mcp` 提供 MCP Streamable HTTP 服务。页面登录密码和 MCP Token 是两套独立鉴权：MCP 客户端只需配置 `GOSTOCK_MCP_TOKEN` 对应的 Bearer Token，不需要先登录网页。

### 1. 服务端配置

在部署机的 `.env` 中生成并填写一个高强度随机 Token，切勿把真实值提交到 Git 仓库：

```bash
openssl rand -hex 32
```

```env
GOSTOCK_MCP_TOKEN=在部署机本地填写生成的Token
```

重建应用容器使配置生效：

```bash
docker compose up -d --force-recreate go-stock
```

MCP 地址按部署方式填写：

- 本机：`http://127.0.0.1:8480/mcp`
- 公网 HTTPS：`https://你的域名/mcp`
- 局域网或直连：`http://服务器IP:8480/mcp`

公网环境应使用 HTTPS，不要通过明文 HTTP 发送 Bearer Token。

### 2. LobeHub 配置

在 LobeHub 中创建“自定义插件 / MCP 插件”，连接方式选择 `Streamable HTTP`（部分版本显示为 `HTTP`），填写：

```text
名称：go-stock MCP
类型：HTTP / Streamable HTTP
URL：https://你的域名/mcp
Header 名：Authorization
Header 值：Bearer <GOSTOCK_MCP_TOKEN>
```

保存前先执行连接测试。正常情况下，客户端应读取到 10 个工具。然后将插件安装到目标助理，并新建一个会话测试，避免旧会话继续使用缓存的工具清单。

对于需要手工维护插件 JSON 的 LobeHub Desktop 版本，MCP 连接参数必须位于运行时实际读取的 `customParams.mcp`，不能只写展示用的 `manifest.mcpParams`：

```json
{
  "identifier": "go-stock-mcp",
  "runtimeType": "mcp",
  "customParams": {
    "mcp": {
      "type": "http",
      "url": "https://你的域名/mcp",
      "headers": {
        "Authorization": "Bearer <GOSTOCK_MCP_TOKEN>"
      }
    }
  }
}
```

推荐优先通过 LobeHub 图形界面的“测试并安装”流程创建插件。该流程会调用 `tools/list`，并自动生成模型需要的 `manifest.api` 工具 Schema。只添加 URL 但没有生成工具清单时，界面可能显示插件已启用，模型却看不到任何函数。

### 3. 命令行验证

以下命令中的 Token 仅从当前终端环境变量读取，避免写入 Shell 历史：

```bash
export GOSTOCK_MCP_URL='https://你的域名/mcp'
read -s GOSTOCK_MCP_TOKEN
export GOSTOCK_MCP_TOKEN
```

初始化连接：

```bash
curl -sS "$GOSTOCK_MCP_URL" \
  -X POST \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H "Authorization: Bearer $GOSTOCK_MCP_TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"curl","version":"1.0"}}}'
```

读取工具清单：

```bash
curl -sS "$GOSTOCK_MCP_URL" \
  -X POST \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H "Authorization: Bearer $GOSTOCK_MCP_TOKEN" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

查询 `601991` 最近 30 个交易日日 K：

```bash
curl -sS "$GOSTOCK_MCP_URL" \
  -X POST \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H "Authorization: Bearer $GOSTOCK_MCP_TOKEN" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_kline","arguments":{"symbol":"601991","period":"day","adjust":"qfq","limit":30}}}'
```

`arguments` 必须是 JSON 对象；`limit` 必须是数字，不能写成字符串 `"30"`。证券代码支持 `601991`、`SH601991`、`601991.SH` 等常见格式。

### 4. 工具清单

只读本地 MySQL/Redis 的分析工具：

| 工具 | 主要参数 | 说明 |
|---|---|---|
| `get_realtime_quote` | `symbol` | 最近一次本地定时行情快照，并非逐秒实时行情 |
| `get_batch_quotes` | `symbols` | 逗号分隔的代码列表，最多 100 只 |
| `get_kline` | `symbol`、`period`、`adjust`、`limit`、`start`、`end` | 日/周/月 K 线，支持前复权和日期范围 |
| `search_stock` | `keyword` | 按代码或名称搜索本地证券 |
| `get_market_indices` | 无 | 最近一次本地指数快照 |
| `get_watchlist` | 无 | 自选股及最近一次本地快照 |
| `get_daily_indicator` | `symbol`、`limit`、`start`、`end` | PE/PB/市值/换手率等每日指标 |
| `get_market_heatmap` | `market`、`group_by`、`metric`、`period`、`limit` | 一级行业或主要概念云图 |
| `get_sync_status` | 无 | 历史数据补齐状态 |

写操作工具：

| 工具 | 主要参数 | 说明 |
|---|---|---|
| `sync_stock_history` | `symbol`、`mode` | 仅按用户明确指令同步单只证券；`latest` 拉最新、`missing` 补缺失、`full` 重建完整历史 |

### 5. 常见问题

- `401 Unauthorized`：检查 Header 是否为完整的 `Authorization: Bearer <token>`，并确认容器已使用新 `.env` 重建。
- 插件显示已启用但模型不调用：确认插件详情中已生成工具 Schema，并新建会话。如果消息记录中的 `tools` 为空，问题在客户端工具注入，不是 go-stock 参数解析。
- `Tool returned no result`：检查 LobeHub 插件运行参数是否包含 `customParams.mcp.type`、`customParams.mcp.url` 和认证 Header。
- `get_kline` 参数错误：确保 `arguments` 是对象，`limit` 是数字，`period` 为 `day/week/month`，`adjust` 为 `qfq/none`。
- 数据日期不是今天：MCP 查询的是本地快照和本地 K 线。先用 `get_sync_status` 查看数据日期，需要时再明确调用单股同步工具。

## 数据说明

- **复权设计**：`kline_daily` 存不复权 OHLCV + `adj_factor`（后复权收盘/不复权收盘）。前复权价 = 原始价 × adj_factor ÷ 最新 adj_factor。除权发生后仅需增量更新，无需重刷全量历史。
- **快照新鲜度**：报价与指数是最近一次 `12:00` 或 `16:00` 的库内快照，不是逐秒实时行情。响应中的 `fetched_at` 为该快照采集时间。
- **历史补齐**：后台只处理缺失或落后最近交易日的数据，页面每 5 秒展示完整、待处理、处理中、失败、部分和空数据数量；完整证券会跳过上游。需要拉取某只证券上市以来完整日K时，也可调用显式单股同步 `mode=full`。
- **分钟分时**：当前没有分钟级本地采集与存储，`/api/v1/timeshare/{code}` 返回 `501`，请使用日K、周K、月K进行分析。

## 目录结构

```
cmd/server/          主入口
internal/
  config/            环境变量配置
  model/             统一数据结构 + symbol 规范化
  provider/          东财/腾讯/新浪数据源 + 熔断降级管理器
  backtest/          A 股交易规则、策略信号和绩效计算
  indicator/         技术指标与策略目录
  store/             MySQL 访问 + embed 迁移
  sync/              历史回填（断点续传）+ 每日增量调度
  api/               REST handlers
  mcpserver/         MCP Streamable HTTP
  cache/             TTL 内存缓存
web/                 Vue3 + Vite + TS + klinecharts（dist embed 进二进制）
```

## 扩展路线

- P1：资金流向、板块行情、龙虎榜、筹码分布（provider 层加接口 + 加表即可）
- P2：财务报表、股东、分红送配
- 市场扩展：`US.AAPL` / `CRYPTO.BTCUSDT` symbol 已预留，新增对应 Provider 实现即可
