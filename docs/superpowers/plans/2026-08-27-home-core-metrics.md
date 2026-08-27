# 首页核心指标精简实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 首页只展示账户总盈亏金额、今日盈亏金额和 0-100 的市场恐惧贪婪指数，完整数据仍在交易与风险板块。

**架构：** 扩展现有 `/trading/account` 返回值，由 store 基于持仓、前收价和今日卖出流水计算 `today_pnl`；扩展 `/risk/gate`，把境内趋势、宽度、VIX 与外盘风险计算为 `market_sentiment`。前端 Home 仅并行加载账户与风险接口，移除推荐统计、图表、持仓和风险细项。

**技术栈：** Go、database/sql、Vue 3、TypeScript、Vite。

---

### 任务 1：今日盈亏后端口径

**文件：**
- 修改：`internal/store/trading.go`
- 测试：`internal/store/trading_test.go`

- [ ] **步骤 1：编写失败测试**

新增纯函数测试，覆盖当日买入持仓直接采用未实现盈亏、隔夜持仓采用当前价减前收价，以及今日卖出扣除费用。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/store -run 'TestPositionTodayPnl|TestSoldTodayPnl'`
预期：因函数未定义而编译失败。

- [ ] **步骤 3：实现账户今日盈亏**

给 `TradeAccount` 增加 `TodayPnl float64`。`TradeAccountOverview` 获取数据库当前日期；持仓当日建仓时加入 `UnrealizedPnl`，隔夜持仓查询该日期前最后收盘价并计算 `(reference-prevClose)*shares`；再查询今日卖出流水，加入 `(sellPrice-prevClose)*shares-fee`。

- [ ] **步骤 4：运行测试验证通过**

运行：`go test ./internal/store`
预期：PASS。

### 任务 2：首页极简视图

**文件：**
- 修改：`web/src/api.ts`
- 修改：`web/src/views/Home.vue`

- [ ] **步骤 1：扩展前端账户类型**

在 `TradeAccount` 增加 `today_pnl: number`。

- [ ] **步骤 2：替换首页数据流与模板**

首页只请求 `tradeAccount()` 和 `riskGate()`；首卡显示总盈亏，次卡显示今日盈亏；风险区域显示 0-100 市场情绪分数、五档标签与恐惧/贪婪刻度，点击导航到风险板块；提供交易明细入口。

- [ ] **步骤 3：收敛响应式样式**

桌面两列金额卡；窄屏改为单列；金额使用固定数字字体并允许动态换行，不显示明细表、图表或额外风险解释。

- [ ] **步骤 4：构建验证**

运行：`cd web && npm run build`
预期：vue-tsc 与 Vite 构建成功。

### 任务 3：整体验证与发布

**文件：**
- 修改：无

- [ ] **步骤 1：全量验证**

运行：`go test -count=1 ./... && go vet ./... && cd web && npm run build`
预期：全部成功。

- [ ] **步骤 2：提交并推送**

提交后推送 `main`，提交信息：`feat(home): 精简账户盈亏与风险灯总览`。

- [ ] **步骤 3：阿里云成都发布**

通过 Tabby 执行 `/opt/go-stock/deploy/release.sh <commit>`，等待发布脚本完成。

- [ ] **步骤 4：线上验收**

确认容器镜像为新 commit、health 为 ok、账户接口包含 `today_pnl`、风险接口可用且启动日志无 ERROR。
