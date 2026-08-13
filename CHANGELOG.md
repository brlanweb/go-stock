# Changelog

All notable changes to this project are documented in this file.

## [Unreleased]

### Added

- Record deterministic shadow-baseline picks (candidate-pool trend-score top 3 and lowest-risk top 3) before every AI recommendation run, compare them with AI picks under the identical five-day frozen window, and expose the comparison via `GET /api/v1/recommendations/shadow-stats` plus an "AI vs baseline" strip on the Recommendations page.
- Exclude candidates that closed at (or near) the exchange limit-up on the analysis day — entry is priced at the next open, so limit-up closes carry the largest gap-up cost (10/20/30 percent caps resolved by board).
- Apply a deterministic overheat penalty to candidate ordering: five-day gains above 15 percent progressively halve the sorting score by 35 percent, without altering the stored trend score or the hard trend/risk filters.
- Enforce sector diversity on AI output: when the candidate pool spans multiple sectors, the three picks must cover at least two sectors or the run is rejected.

### Changed

- Align the recommendation objective with the scoring window: prompts (inline default and `config/ai_prompt.md`) now target relative five-trading-day performance — next-open entry, fifth-close settlement — instead of ten-day trend continuation, weight short-term momentum, price-volume confirmation, and pullback entries, and warn against chasing overheated names.

- Add a trading-day 17:00 daily review pipeline based only on local close data: indices, market breadth, strong and weak sectors, pre-market hotspot outcomes, and the latest five recommendation days.
- Persist deterministic review facts and structured AI reports as append-only history with market phase, hotspot verification, recommendation attribution, previous-directive verification, risk controls, and optimization directives.
- Compare recommendation-window returns with a CSI 300 close-based benchmark (falling back to the SSE Composite when unavailable) and expose benchmark, excess-return, and tracking-freeze status.
- Automatically inject up to five validated directives from the latest review into the next 08:10 AI trend recommendation while preserving the fixed candidate pool and risk constraints.
- Add daily review REST endpoints and a responsive Vue review workspace with history, manual runs, sector analysis, recommendation attribution, and risk controls.
- Auto-adjust the recommendation candidate risk cap by the latest review market phase (up 85 / range 75 / down 65, base 70) with no manual configuration, and expose it via `GET /api/v1/recommendations/risk-policy` plus the Recommendations page header.
- Feed review `risk_controls` (position mode and avoid conditions) into the next-day recommendation prompt as ranking preferences alongside directives.
- Add a deterministic market stance indicator (take_profit 落袋 / hold 扛单 / accumulate 扫货) derived from a 20-day equal-weight market replay of local history — momentum, drawdown, rebound, and breadth — displayed prominently on the review page; the AI must reference it and cannot rewrite it.

### Changed

- Rebalance the deterministic risk score to stop over-penalizing strong trends: volatility 40, max drawdown 45 (was 35), short-term overheat 15 with a 35% five-day gain cap (was 25 at 25%).
- Relax the recommendation candidate count from exactly 10 to 5-10 so risk filtering in weak markets no longer aborts the daily run; prompts now state the actual candidate count.
- Run manual recommendation generation asynchronously with a five-minute budget and a `GET /api/v1/recommendations/status` polling endpoint (the previous synchronous handler timed out at 30 seconds); the Recommendations page now polls until completion and surfaces failures.
- Give the recommendation AI request a dedicated four-minute HTTP timeout matching the scheduler budget.

### Database

- Add migration `016_daily_review.sql` and the `daily_review` history table.
- Add migration `017_recommendation_shadow.sql` and the `recommendation_shadow` baseline table.

## [1.1.1] - 2026-08-04

### Fixed

- Restore realtime volume as a background overlay while keeping dedicated volume panes for daily, weekly, and monthly candlestick charts.
- Prevent long-range monthly price scales from extending below zero by dynamically limiting the main price pane's bottom gap.
- Preserve stock, period, and request-version isolation during asynchronous chart loads, and show errors only for the current request.

### Database

- No database schema or DML changes relative to v1.1.0.

## [1.1.0] - 2026-08-04

### Added

- Add realtime intraday candlestick charts for stock detail pages.
- Add EMA12 and RSI14 indicators to all chart periods.
- Expand the catalog of executable backtest strategies.
- Persist watchlist snapshots and constrain AI recommendation picks.

### Fixed

- Prevent stale chart responses from overwriting the currently selected stock or period, including a latest-version request deduplication fix for rapid switching.
- Keep the price axis independent from background volume data.
- Restore market heatmap data and post-close watchlist snapshot rendering.
- Show live quotes in stock detail pages and stabilize sidebar quick search input.
- Publish watchlist quotes from realtime Redis batches.
- Support executable backtests for high-priced stocks.
- Restore deterministic daily AI recommendations and the AI trend recommendation pipeline.
- Exclude ETFs from automatic history backfill and finalize inactive backfill jobs.
- Use a reachable Go module proxy in image builds.
- Clarify MCP tool arguments and document MCP setup.

[1.1.1]: https://github.com/brlanweb/go-stock/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/brlanweb/go-stock/releases/tag/v1.1.0
