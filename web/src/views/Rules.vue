<script setup lang="ts">
const sections = [
  { id: 'overview', label: '系统目标' },
  { id: 'schedule', label: '每日流程' },
  { id: 'funnel', label: '热点与候选' },
  { id: 'recommendation', label: 'AI 三只推荐' },
  { id: 'entry', label: '入池与建仓' },
  { id: 'intraday', label: '盘中分析' },
  { id: 'risk', label: '确定性风控' },
  { id: 'ai-actions', label: 'AI 持仓动作' },
  { id: 'lifecycle', label: '状态与记账' },
  { id: 'statistics', label: '统计口径' },
  { id: 'limits', label: '限制与风险' },
]
</script>

<template>
  <main class="rules-page">
    <header class="rules-topbar">
      <RouterLink to="/" class="back-link" aria-label="返回首页"><span aria-hidden="true">←</span> 返回首页</RouterLink>
      <span>规则档案 · v1.4.1</span>
    </header>

    <div class="rules-layout">
      <aside class="rules-nav" aria-label="规则目录">
        <strong>规则目录</strong>
        <nav>
          <a v-for="(item, index) in sections" :key="item.id" :href="`#${item.id}`">
            <span>{{ String(index + 1).padStart(2, '0') }}</span>{{ item.label }}
          </a>
        </nav>
      </aside>

      <article class="rules-document">
        <header class="document-title">
          <p>GO-STOCK / TREND LIFECYCLE</p>
          <h1>A 股趋势交易规则档案</h1>
          <div class="title-meta">
            <span>规则版本 v1.4.1</span>
            <span>时区 Asia/Shanghai</span>
            <span>适用范围：沪深 A 股趋势推荐</span>
          </div>
          <p class="lead">系统将本地行情筛选、AI 受限排序和确定性风险控制组成可追溯的交易生命周期。目标是在胜率、盈利能力与风险之间取得可验证的平衡，不承诺收益，也不替代真实账户的交易决策。</p>
        </header>

        <section id="overview" class="rule-section">
          <div class="section-no">01</div>
          <div>
            <h2>系统目标与执行边界</h2>
            <p>系统处理的是短中期趋势机会，不按固定五日强制退出。候选生成、建仓等待、持仓管理和退出结算分别记录，任何一只股票都必须经过明确状态流转后才能进入真实交易统计。</p>
            <div class="principles">
              <span><b>事实层</b>本地 MySQL 日 K、实时股票与指数行情、板块数据</span>
              <span><b>判断层</b>受候选池约束的 AI 推荐与三层盘中分析</span>
              <span><b>纪律层</b>确定性风控生成高优先级建议，用户确认后执行真实平仓</span>
              <span><b>审计层</b>推荐、AI/规则建议、手动建仓和平仓结果均留痕</span>
            </div>
          </div>
        </section>

        <section id="schedule" class="rule-section">
          <div class="section-no">02</div>
          <div>
            <h2>每个交易日的闭环</h2>
            <ol class="timeline">
              <li><time>16:00</time><div><b>收盘数据同步</b><p>保存全天市场快照并更新日 K、指标和板块统计。盘前任务要求最近日 K 不早于上一交易日。</p></div></li>
              <li><time>17:00</time><div><b>每日复盘</b><p>复核指数、市场宽度、板块、热点兑现和最近推荐，生成市场阶段、可执行优化指令及次日风险偏好。</p></div></li>
              <li><time>08:00</time><div><b>热点漏斗</b><p>基于最近收盘数据执行数据筛选、关系候选、AI 产业链分析和本地数据回验。</p></div></li>
              <li><time>08:10</time><div><b>趋势推荐</b><p>从确定性候选池中由 AI 恰好选出 3 只，仅将唯一最强标的加入建仓池。</p></div></li>
              <li><time>盘中</time><div><b>建仓与持仓分析</b><p>8 个固定时点批量获取实时股票和指数行情，本地风控与 AI 只记录建议，真实建仓和平仓由用户手动确认。</p></div></li>
            </ol>
          </div>
        </section>

        <section id="funnel" class="rule-section">
          <div class="section-no">03</div>
          <div>
            <h2>热点漏斗与候选池</h2>
            <p>热点漏斗不是简单按涨幅排名。系统先对概念板块统计进行筛选，再基于成分重叠形成关系候选，AI 只能在输入概念中识别产业主线和卡点，最后由本地数据重新验证有效性。</p>
            <ul>
              <li>一级筛选最多读取 30 个概念；有效概念成分股数量为 5 至 150 只。</li>
              <li>AI 保留 2 至 4 条主线，每条 1 至 3 个卡点概念，并给出证伪条件。</li>
              <li>推荐优先复用当日热点漏斗的卡点概念成分；样本不足时回退到题材热度候选池。</li>
              <li>最终可分析候选至少 5 只；每只必须具备完整的最近 60 根前复权日 K。</li>
            </ul>
            <h3>趋势与风险的确定性过滤</h3>
            <div class="formula-grid">
              <div><b>趋势成立</b><span>收盘价 &gt; MA5 &gt; MA20 &gt; MA60，MA20 向上，近 5/20/60 日收益均为正。</span></div>
              <div><b>趋势评分</b><span>60 日收益 55% + 20 日收益 30% + 5 日收益 15%，并加入 MA20 相对 MA60 的结构强度。</span></div>
              <div><b>风险评分</b><span>年化波动率 40% + 60 日最大回撤 45% + 近 5 日过热 15%；分数越高风险越大。</span></div>
              <div><b>过热处理</b><span>近 5 日涨幅超过 15% 开始降权，35% 达到最大惩罚；昨日接近涨停的高开风险候选直接剔除。</span></div>
            </div>
          </div>
        </section>

        <section id="recommendation" class="rule-section">
          <div class="section-no">04</div>
          <div>
            <h2>AI 三只趋势推荐</h2>
            <p>AI 是受限评审器，不负责扩大股票范围。它只能从后端给出的候选中选股，所有返回代码、板块、概率和数量都由后端再次校验。</p>
            <ul>
              <li>必须恰好返回 3 只且代码不重复，理由不超过 80 字。</li>
              <li>按趋势持续性与可建仓性排序，兼顾结构完整、建仓空间和短期过热风险。</li>
              <li>候选覆盖多个板块时，3 只不得全部来自同一板块。</li>
              <li>前一日复盘指令只能调整候选内的相对排序和风险偏好，不能绕过硬过滤。</li>
              <li>市场阶段为上涨时兼顾趋势与风险；震荡时提高确认要求；下跌时优先低风险、低过热和回撤控制。</li>
            </ul>
          </div>
        </section>

        <section id="entry" class="rule-section">
          <div class="section-no">05</div>
          <div>
            <h2>生命周期入池与建仓</h2>
            <p>每日从三只推荐中只选择唯一最强标的进入 <code>pending_entry</code>。排序顺序为 AI 概率降序、风险分升序、AI 排名升序。活跃自选容量上限为 10 只，容量不足时不会静默淘汰仍在等待或持有的旧标的。</p>
            <div class="parameter-table">
              <div><span>入池数量</span><b>每日唯一 1 只</b><small>只取唯一最强</small></div>
              <div><span>建仓宽限</span><b>D0 至 D0+2</b><small>按后续交易日计算</small></div>
              <div><span>执行方式</span><b>用户手动确认</b><small>AI 区间仅作参考</small></div>
              <div><span>过期处理</span><b>转为 expired</b><small>移出活跃自选且不计收益</small></div>
            </div>
            <p class="note">AI 给出的建仓价格只是参考区间，不会自动触发建仓。用户点击建仓或平仓时，系统按当时可用行情记录参考价，不等同于券商真实成交回报。</p>
          </div>
        </section>

        <section id="intraday" class="rule-section">
          <div class="section-no">06</div>
          <div>
            <h2>盘中分析时点与上下文</h2>
            <p>交易日固定执行 8 档分析：<strong>10:00、10:30、11:00、11:30、13:30、14:00、14:30、14:52</strong>。开盘前 30 分钟不做自动动作，尾盘档用于确认需要接近收盘条件的趋势破位。</p>
            <div class="context-grid">
              <div><span>大盘</span><p>至少 3 个指数的涨跌、整体平均涨幅、上涨与下跌数量及行情时间戳。</p></div>
              <div><span>板块</span><p>标的所属板块强弱、板块与大盘是否同步转弱，以及热点逻辑是否仍成立。</p></div>
              <div><span>个股</span><p>实时价格、建仓价、最高/最低价、持有日数、仓位、ATR14、MA10/MA20 和浮盈回撤。</p></div>
            </div>
            <p class="note">系统使用行情源时间戳判断真实交易日期。指数时间戳采用多数票，降低单个陈旧行情源导致误判休市的风险。</p>
          </div>
        </section>

        <section id="risk" class="rule-section">
          <div class="section-no">07</div>
          <div>
            <h2>确定性风控建议优先级</h2>
            <p>每个盘中档先按下列顺序检查，命中后记录为高优先级建议，并继续保留 AI 独立评估结果；两者都不会自动改变真实仓位。</p>
            <ol class="risk-list">
              <li><span>01</span><div><b>ATR 自适应硬止损</b><p>止损距离取 <code>max(6%, ATR14 × 1.8)</code>，上限 10%。相对建仓价达到该亏损即全部退出。</p></div></li>
              <li><span>02</span><div><b>系统性风险</b><p>至少 3 个指数有效；下跌指数占比达到 2/3，且指数平均跌幅不高于 -1.5% 时退出。</p></div></li>
              <li><span>03</span><div><b>移动止盈</b><p>最高浮盈达到 5% 后激活；当前浮盈较最高浮盈回撤 4 个百分点时退出锁定利润。</p></div></li>
              <li><span>04</span><div><b>目标止盈</b><p>浮盈达到 12% 时减仓 50%；剩余仓位交由移动止盈管理。仓位已不高于 30% 时直接退出。</p></div></li>
              <li><span>05</span><div><b>时间止损</b><p>持有达到 3 个交易日，浮盈仍低于 3% 时退出，释放被低效占用的仓位。</p></div></li>
              <li><span>06</span><div><b>尾盘趋势破位</b><p>仅在 14:52 检查。现价低于 MA10 的 99%，并且板块偏弱或大盘平均涨幅为负时退出。</p></div></li>
              <li><span>07</span><div><b>最长持有兜底</b><p>达到 15 个交易日仍未被其他规则处理时退出，不让单一标的长期占用容量。</p></div></li>
            </ol>
          </div>
        </section>

        <section id="ai-actions" class="rule-section">
          <div class="section-no">08</div>
          <div>
            <h2>AI 的持仓动作</h2>
            <p>只有在确定性风控未触发时，AI 才结合大盘、板块和个股三层上下文判断。</p>
            <div class="action-row">
              <div><code>hold</code><p>趋势和风险仍可接受，建议继续持有并在下一盘中档复核。</p></div>
              <div><code>reduce</code><p>建议减仓保护收益，只记录理由和参考价格区间，不自动改变仓位。</p></div>
              <div><code>exit</code><p>建议尽快平仓；用户手动确认后才冻结收益并释放自选容量。</p></div>
            </div>
            <p>AI 输入包含浮盈、最高浮盈、止损价、当前仓位和 ATR；模型负责形成独立分析记录，用户结合规则与 AI 建议作最终交易决策。</p>
          </div>
        </section>

        <section id="lifecycle" class="rule-section">
          <div class="section-no">09</div>
          <div>
            <h2>状态机与交易记账</h2>
            <div class="state-flow" aria-label="持仓状态流转">
              <div class="state-path"><span>pending_entry</span><i>建仓</i><span>holding</span><i>退出</i><span>exited</span></div>
              <div class="state-path short"><span>pending_entry</span><i>宽限过期</i><span>expired</span></div>
            </div>
            <ul>
              <li><code>pending_entry</code>：已入池、尚未出现可执行建仓点；不计收益。</li>
              <li><code>holding</code>：已记录建仓参考价；只展示浮动收益，不进入已实现胜率。</li>
              <li><code>exited</code>：用户手动确认平仓；平仓参考价和净收益冻结，后续行情不再改变结果。</li>
              <li><code>expired</code>：建仓宽限内未成交；不计收益、不计胜率。</li>
              <li>退出状态变更和自选移除在同一数据库事务中完成，避免已退出标的继续占位。</li>
            </ul>
            <p class="note">生命周期收益估算扣除 0.25% 往返交易成本，包含佣金、印花税、过户费与滑点的合并假设。发生减仓时按已实现部分和剩余仓位加权。</p>
          </div>
        </section>

        <section id="statistics" class="rule-section">
          <div class="section-no">10</div>
          <div>
            <h2>三种统计口径严格隔离</h2>
            <div class="stats-table">
              <div class="stats-head"><span>口径</span><span>样本</span><span>用途</span><span>不能解释为</span></div>
              <div><b>真实生命周期</b><span>有 position 记录的 holding / exited</span><span>真实策略浮盈、已实现收益与胜率</span><span>券商账户实际成交收益</span></div>
              <div><b>历史参考走势</b><span>未加入建仓池的推荐</span><span>次日开盘至第 10 个交易日收盘</span><span>真实建仓或真实平仓</span></div>
              <div><b>每日三只组合</b><span>每个推荐日 3 只等权</span><span>唯一最强用手动交易结果，其余两只用固定 10 日结果</span><span>账户净值或实际持仓组合</span></div>
            </div>
            <ul>
              <li>真实胜率分母只包含 <code>exited</code>；没有真实退出样本时显示 0.0% 并明确提示无样本。</li>
              <li>未加入建仓池的两只推荐从次日开盘开始，在第 10 个交易日收盘冻结；窗口未满时随最新收盘变化，始终不进入真实交易统计。</li>
              <li>“收益点数合计”是多笔个股收益率百分点相加，不是按本金、仓位和复利计算的账户收益率。</li>
              <li>图表采用 A 股红涨绿跌：零轴以上红色、零轴以下绿色。</li>
            </ul>
          </div>
        </section>

        <section id="limits" class="rule-section final-section">
          <div class="section-no">11</div>
          <div>
            <h2>已知限制与风险声明</h2>
            <ul>
              <li>AI 概率是模型排序信息，不是可校准的真实上涨概率。</li>
              <li>行情源延迟、停牌、涨跌停、流动性和滑点都可能使实际成交偏离参考价。</li>
              <li>当前交易成本是固定估算，未按账户佣金、最低佣金和实际成交量逐笔计算。</li>
              <li>节假日判断以行情源交易日期和工作日调度为基础，异常数据源仍可能造成任务跳过。</li>
              <li>风控参数基于工程化风险约束，仍需更多跨市场阶段样本、滚动回测和真实成交审计验证。</li>
              <li>历史参考只用于规则复盘；不得将其重新混入真实生命周期指标。</li>
            </ul>
            <div class="risk-notice"><b>风险提示</b><p>本系统是研究和辅助决策工具。任何规则都无法保证盈利，历史结果不代表未来表现。使用者应结合自身资金规模、风险承受能力和真实交易约束独立决策。</p></div>
          </div>
        </section>
      </article>
    </div>
  </main>
</template>

<style scoped>
.rules-page{min-height:100vh;background:#0e1724;color:#dfe6ef;letter-spacing:0}.rules-topbar{position:sticky;top:0;z-index:10;display:flex;height:48px;align-items:center;justify-content:space-between;padding:0 24px;border-bottom:1px solid #29364a;background:#111c2c;color:#7f8da3;font-size:11px}.back-link{display:inline-flex;align-items:center;gap:7px;color:#b9c8db;font-size:12px}.back-link:hover,.back-link:focus-visible{color:#fff;outline:none}.rules-layout{display:grid;grid-template-columns:220px minmax(0,880px);gap:44px;justify-content:center;padding:42px 32px 72px}.rules-nav{position:sticky;top:78px;align-self:start;border-top:2px solid #526a89}.rules-nav>strong{display:block;padding:14px 8px 10px;color:#eef3f8;font-size:12px}.rules-nav nav{display:grid}.rules-nav a{display:grid;grid-template-columns:28px 1fr;gap:7px;padding:7px 8px;border-left:2px solid transparent;color:#8492a8;font-size:11px}.rules-nav a span{color:#56657c;font-variant-numeric:tabular-nums}.rules-nav a:hover,.rules-nav a:focus-visible{border-left-color:#7596bd;background:#172437;color:#d8e3f0;outline:none}.rules-document{min-width:0}.document-title{padding:4px 0 38px;border-bottom:1px solid #354258}.document-title>p:first-child{margin:0 0 12px;color:#7596bd;font-size:10px;font-weight:700}.document-title h1{margin:0;color:#f2f5f8;font-size:34px;font-weight:700;line-height:1.2}.title-meta{display:flex;flex-wrap:wrap;gap:8px 18px;margin-top:18px;color:#75849a;font-size:10px}.lead{max-width:760px;margin:24px 0 0;color:#b0bbca;font-size:14px;line-height:1.85}.rule-section{display:grid;grid-template-columns:48px minmax(0,1fr);gap:22px;padding:38px 0;border-bottom:1px solid #29364a;scroll-margin-top:62px}.section-no{padding-top:4px;color:#60718a;font-size:11px;font-variant-numeric:tabular-nums}.rule-section h2{margin:0 0 17px;color:#edf2f7;font-size:21px}.rule-section h3{margin:25px 0 12px;color:#cdd7e3;font-size:14px}.rule-section p{margin:0 0 14px;color:#aeb9c8;font-size:13px;line-height:1.8}.rule-section strong{color:#dfe7f0}.rule-section ul{display:grid;gap:9px;margin:14px 0 0;padding:0;list-style:none}.rule-section ul li{position:relative;padding-left:16px;color:#aeb9c8;font-size:12px;line-height:1.7}.rule-section ul li::before{position:absolute;top:9px;left:0;width:5px;height:5px;background:#6d87a8;content:""}.rule-section code{padding:1px 4px;background:#1a283b;color:#9cc0e6;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.92em}.principles{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));border-top:1px solid #2c394d;border-left:1px solid #2c394d}.principles span{display:flex;min-height:82px;flex-direction:column;gap:7px;padding:14px;border-right:1px solid #2c394d;border-bottom:1px solid #2c394d;color:#98a5b7;font-size:11px;line-height:1.55}.principles b{color:#d8e1eb;font-size:12px}.timeline{display:grid;gap:0;margin:0;padding:0;list-style:none}.timeline li{display:grid;grid-template-columns:62px 1fr;gap:15px;padding:13px 0;border-top:1px solid #29364a}.timeline time{padding-top:2px;color:#7899bf;font-size:11px;font-weight:700;font-variant-numeric:tabular-nums}.timeline b{display:block;margin-bottom:4px;font-size:12px}.timeline p{margin:0;font-size:11px}.formula-grid,.context-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));border-top:1px solid #2c394d;border-left:1px solid #2c394d}.formula-grid>div,.context-grid>div{padding:14px;border-right:1px solid #2c394d;border-bottom:1px solid #2c394d}.formula-grid b,.context-grid span{display:block;margin-bottom:7px;color:#d7e1ec;font-size:12px}.formula-grid span,.context-grid p{color:#96a5b8;font-size:11px;line-height:1.65}.context-grid{grid-template-columns:repeat(3,minmax(0,1fr))}.context-grid p{margin:0}.parameter-table{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));margin:20px 0;border-top:1px solid #2f3d52;border-left:1px solid #2f3d52}.parameter-table>div{display:flex;min-height:102px;flex-direction:column;gap:6px;padding:13px;border-right:1px solid #2f3d52;border-bottom:1px solid #2f3d52}.parameter-table span,.parameter-table small{color:#7f8da1;font-size:10px}.parameter-table b{margin-top:auto;color:#dce5ee;font-size:12px;line-height:1.45}.note{padding:11px 13px;border-left:3px solid #607e9f;background:#152235;color:#96a7ba!important;font-size:11px!important}.risk-list{display:grid;gap:0;margin:18px 0 0;padding:0;list-style:none;border-top:1px solid #2c394d}.risk-list li{display:grid;grid-template-columns:38px 1fr;gap:12px;padding:13px 0;border-bottom:1px solid #2c394d}.risk-list>li>span{display:grid;width:28px;height:28px;place-items:center;border:1px solid #40516a;color:#7899bf;font-size:10px}.risk-list b{display:block;margin-bottom:3px;color:#dce5ee;font-size:12px}.risk-list p{margin:0;font-size:11px}.action-row{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));margin:18px 0;border-top:1px solid #2c394d;border-left:1px solid #2c394d}.action-row>div{padding:16px;border-right:1px solid #2c394d;border-bottom:1px solid #2c394d}.action-row code{display:inline-block;margin-bottom:9px;font-weight:700}.action-row p{margin:0;font-size:11px}.state-flow{display:grid;gap:9px;margin:18px 0}.state-path{display:grid;grid-template-columns:auto 58px auto 58px auto;gap:8px;align-items:center}.state-path.short{grid-template-columns:auto 58px auto;max-width:430px}.state-path span{padding:9px 10px;border:1px solid #40516a;background:#172438;color:#c6d3e2;text-align:center;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:10px}.state-path i{color:#6f8098;font-size:9px;font-style:normal;text-align:center}.stats-table{margin:18px 0;border-top:1px solid #314055;border-left:1px solid #314055}.stats-table>div{display:grid;grid-template-columns:130px 1.2fr 1.2fr 1fr}.stats-table span,.stats-table b{padding:10px;border-right:1px solid #314055;border-bottom:1px solid #314055;color:#9caabc;font-size:10px;line-height:1.55}.stats-table b{color:#d7e1ec}.stats-head span{background:#172437;color:#78889f;font-weight:700}.final-section{border-bottom:0}.risk-notice{margin-top:24px;padding:16px;border:1px solid #65454a;background:#271d25}.risk-notice b{display:block;margin-bottom:6px;color:#e68c94;font-size:12px}.risk-notice p{margin:0;color:#c6aeb1;font-size:11px}.rules-document *{letter-spacing:0}@media(max-width:900px){.rules-layout{grid-template-columns:1fr;padding:28px 24px 60px}.rules-nav{display:none}.rules-topbar{padding:0 20px}.parameter-table{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:600px){.rules-topbar{height:44px;padding:0 14px}.rules-layout{padding:24px 15px 48px}.document-title{padding-bottom:28px}.document-title h1{font-size:27px}.title-meta{display:grid;gap:5px}.lead{font-size:13px}.rule-section{grid-template-columns:30px minmax(0,1fr);gap:10px;padding:30px 0;scroll-margin-top:52px}.rule-section h2{font-size:18px}.principles,.formula-grid,.context-grid,.action-row{grid-template-columns:1fr}.parameter-table{grid-template-columns:repeat(2,minmax(0,1fr))}.parameter-table>div{min-height:92px}.state-path{grid-template-columns:1fr 42px 1fr}.state-path i:nth-of-type(2){grid-column:2}.state-path.short{grid-template-columns:1fr 42px 1fr;max-width:none}.stats-table{overflow-x:auto}.stats-table>div{min-width:640px}.document-title,.rule-section{min-width:0}}
</style>
