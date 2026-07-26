# go-stock

> 低内存 A 股数据底座：Go 单二进制集成 REST API、MCP Streamable HTTP、Vue 3 市场云图和 MySQL 历史数据同步。

[![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)](https://go.dev/) [![Vue](https://img.shields.io/badge/Vue-3-42B883?logo=vuedotjs)](https://vuejs.org/) [![MySQL](https://img.shields.io/badge/MySQL-5.7%2B-4479A1?logo=mysql)](https://www.mysql.com/)

## 设计边界

- **查询只读本地库**：页面、REST 查询端点和 MCP 分析工具只读取 MySQL；不会因东方财富等上游瞬断在浏览器或 AI 分析时返回 `EOF`。
- **外部数据仅后台采集**：交易日 `12:00`（上午收盘）和 `16:00`（全天收盘）按 `Asia/Shanghai` 定时采集全市场快照；手动同步和缺失补齐属于显式写操作。
- **快照可追溯**：报价和指数响应携带 `fetched_at`，代表最近一次本地快照的采集时间，不将缓存快照伪装成实时数据。
- **云图按板块分区**：默认筛选热度靠前 100 个行业或概念、每板块最多 50 只证券；先计算板块矩形，再在板块内部按市值计算个股矩形，避免大市值个股跨板块覆盖。颜色由所选指标决定。
- **流式个股 Agent**：详情页右侧抽屉支持流式回答、按证券保存 MySQL 对话历史，并可选择是否携带当前快照及 `0/10/30/60` 个交易日日 K；历史条数由后端校验和截取。

## 特性

- **本地市场快照**：交易日 `12:00` 保存上午快照、`16:00` 保存收盘快照；报价、指数、自选股、页面和 MCP 分析工具均读取最近一次 MySQL 快照
- **后台采集降级**：实时行情东方财富（全字段）→ 腾讯 → 新浪；历史K线东方财富 → BaoStock → AKShare → 腾讯，自动降级 + 熔断 + 限流抖动
- **行情维度**：价格/涨跌/开高低昨收/量额/量比/换手/振幅/PE动/PB/总市值/流通市值/52周高低/买卖五档/数据源与时间戳元数据
- **历史K线**：不复权原始数据 + 累积复权因子完整入库（前复权随时重算，除权无需重刷）；日线存储，周/月线 SQL 聚合
- **受控缺失补齐**：`sync_checkpoint` 表记录进度，历史为空或落后最近交易日时低速补齐；完整证券不重复请求上游。历史源按 `东方财富 → BaoStock → AKShare → 腾讯` 降级：BaoStock 覆盖沪深股票/ETF，AKShare（新浪源）覆盖 ETF 与北交所 `920` 代码，避免单一上游被限流时整批失败
- **定时快照**：按 `Asia/Shanghai` 在工作日 `12:00` 采集上午快照，`16:00` 采集收盘快照并写入日K/每日指标
- **MCP Streamable HTTP**（`/mcp`）：本地 MySQL 分析工具 + 显式单股同步工具，供 LobeHub / Claude 等 MCP 客户端调用
- **Vue3 前端**：全屏市场终端云图，面积按总市值映射、颜色按涨跌幅映射；个股日K/周K/月K 图（klinecharts）
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
| GOSTOCK_AI_BASE_URL | 空 | OpenAI 兼容 API 基础地址，如 `https://api.openai.com/v1` |
| GOSTOCK_AI_API_KEY | 空 | 模型 API Key，仅写入本地或服务器 `.env` |
| GOSTOCK_AI_MODEL | 空 | 模型正式标识，如服务商实际提供的模型 ID |
| GOSTOCK_AI_PROMPT_FILE | config/ai_prompt.md | AI 趋势推荐提示词文件路径（长提示词放文件，配置只填路径） |
| GOSTOCK_AI_PROMPT | 空 | 可选内联短提示词；留空则用提示词文件，再空则用内嵌默认 |

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
| `GET /api/v1/timeshare/{code}` | 当前未启用分钟级本地快照，返回 `501` |
| `GET /api/v1/search?q=茅台` | 仅搜索本地证券基础信息 |
| `GET /api/v1/indices` | 最近一次本地指数快照 |
| `GET /api/v1/security/{code}` | 基础信息 |
| `GET /api/v1/indicator/{code}` | 每日指标历史 |
| `GET /api/v1/market/heatmap?market=all&group_by=industry&metric=change_pct&period=1d&limit=100` | 本地日K与指标生成的行业/概念云图；默认前 100 个热度板块，每板块最多 50 只 |
| `POST /api/v1/agent/chat/stream` | 个股 Agent SSE 流式对话；`history_days` 仅支持 `0/10/30/60`，`include_stock` 控制是否携带个股快照 |
| `GET/DELETE /api/v1/agent/chat/history/{code}` | 查询或清除当前证券保存在 MySQL 的 Agent 对话历史 |
| `GET/POST/DELETE /api/v1/watchlist[/{code}]` | 自选股与本地快照 |
| `GET /api/v1/sync/status` | 回填进度 |
| `POST /api/v1/sync/stock/{code}?mode=latest\|missing\|full` | 显式同步单只证券 |
| `POST /api/v1/sync/backfill` | 启动受控的全市场缺失历史补齐（完整证券跳过上游） |
| `POST /api/v1/sync/backfill/stop` | 停止历史补齐并保留断点 |
| `POST /api/v1/sync/daily` | 手动触发本地市场快照采集 |

## MCP 接入（LobeHub）

端点：`http://<host>:8480/mcp`（Streamable HTTP）

LobeHub 添加自定义 MCP 插件：
- 类型：Streamable HTTP
- URL：`http://服务器IP:8480/mcp`
- 若设置了 `GOSTOCK_MCP_TOKEN`，Header 加 `Authorization: Bearer <token>`

分析工具（只读本地 MySQL）：
`get_realtime_quote` / `get_batch_quotes` / `get_kline` / `search_stock` / `get_market_indices` / `get_watchlist` / `get_daily_indicator` / `get_market_heatmap` / `get_sync_status`

写操作：`sync_stock_history`。该工具仅按用户明确指令同步单只证券，支持 `latest`、`missing`、`full` 三种模式。

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
