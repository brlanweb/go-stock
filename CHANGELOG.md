# Changelog

All notable changes to this project are documented in this file.

## [Unreleased]

## [1.2.5] - 2026-08-15

### Fixed

- Render the historical reference aggregate as return points (`点`) instead of a percentage, preventing the legacy sum from being mistaken for an account return rate.

### Database

- No database schema or DML changes relative to v1.2.4.

## [1.2.4] - 2026-08-15

### Fixed

- Show `0.0%` with an explicit “no real exits” message instead of an unexplained dash when the actual lifecycle has not exited any positions yet.
- Add a separately labeled historical reference win rate and reference return-point total to Home and Recommendations, using only legacy trend-rule results and never mixing them into actual lifecycle performance.
- Preserve the actual-trading contract: only real `position` exits affect the official win rate, realized return, and equity curve.

### Database

- No database schema or DML changes relative to v1.2.3.

## [1.2.3] - 2026-08-15

### Fixed

- Backfill the latest pre-lifecycle recommendation batch into one `pending_entry` position when it directly precedes the newest stored trading day, so the Home dashboard no longer remains disconnected from valid current recommendations after upgrading from a pre-lifecycle release.
- Keep the backfill non-transactional in performance terms: it records no entry price or return, and still requires a real intraday AI `entry` decision before any holding or performance statistic appears.
- Clarify the Home dashboard state while recommendations are waiting for an actual entry signal instead of presenting an unexplained empty return chart.

### Database

- Add migration `020_backfill_latest_position.sql` with freshness, idempotency, and watchlist-capacity guards.

## [1.2.2] - 2026-08-15

### Fixed

- Restore reference entry/latest prices and change percentages for legacy recommendation history while keeping those rows marked `reference_only` and excluded from lifecycle performance statistics.

## [1.2.1] - 2026-08-15

### Fixed

- Restrict official trend-trading performance to persisted position lifecycles: legacy recommendations without a position no longer enter win rate, realized return, floating return, or equity-curve statistics.
- Separate exited realized performance from holding unrealized performance, add lifecycle status counts and detailed position rows, and label summed trade-return points as a strategy diagnostic rather than an account return.
- Remove mixed actual/simulated performance panels from the Home and Recommendations views; legacy recommendation rows remain available for historical review and are explicitly marked as recommendation-only records.

### Database

- No database schema or DML changes relative to v1.2.0.

## [1.2.0] - 2026-08-15

### Added

- Record deterministic shadow-baseline picks (candidate-pool trend-score top 3 and lowest-risk top 3) before every AI recommendation run, compare them with AI picks under the same trend-exit window, and expose the comparison via `GET /api/v1/recommendations/shadow-stats` plus an "AI vs baseline" strip on the Recommendations page.
- Add a persisted recommendation lifecycle (`pending_entry` / `holding` / `exited` / `expired`): one top-ranked pick enters the watchlist each trading day, gets up to two following trading days to find an AI entry range, then receives 30-minute market/sector/stock exit analysis until the trend breaks. Exits free the watchlist slot immediately while remaining visible in recommendation history with frozen realized performance.
- Add `GET /api/v1/positions` and extend intraday advice with stage, price range, urgency, and reference-price fields; the Home and Recommendations views now show active positions, entry/exit signals, and lifecycle-aware statistics.
- Exclude candidates that closed at (or near) the exchange limit-up on the analysis day — entry is priced at the next open, so limit-up closes carry the largest gap-up cost (10/20/30 percent caps resolved by board).
- Apply a deterministic overheat penalty to candidate ordering: five-day gains above 15 percent progressively halve the sorting score by 35 percent, without altering the stored trend score or the hard trend/risk filters.
- Enforce sector diversity on AI output: when the candidate pool spans multiple sectors, the three picks must cover at least two sectors or the run is rejected.

### Changed

- Align the recommendation objective with lifecycle trend trading: rank candidates for sustainable structure and executable intraday entry space, then use actual AI entry/exit reference prices for the selected pick. Unentered picks do not affect performance; AI exits freeze performance instead of continuing to accrue.

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
- Add migration `018_entry_advice.sql` for persisted intraday AI entry advice.
- Add migration `019_position_lifecycle.sql` for position lifecycle state and entry/exit advice fields.

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

[Unreleased]: https://github.com/brlanweb/go-stock/compare/v1.2.5...HEAD
[1.2.5]: https://github.com/brlanweb/go-stock/compare/v1.2.4...v1.2.5
[1.2.4]: https://github.com/brlanweb/go-stock/compare/v1.2.3...v1.2.4
[1.2.3]: https://github.com/brlanweb/go-stock/compare/v1.2.2...v1.2.3
[1.2.2]: https://github.com/brlanweb/go-stock/compare/v1.2.1...v1.2.2
[1.2.1]: https://github.com/brlanweb/go-stock/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/brlanweb/go-stock/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/brlanweb/go-stock/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/brlanweb/go-stock/releases/tag/v1.1.0
document MCP setup.

[Unreleased]: https://github.com/brlanweb/go-stock/compare/v1.2.1...HEAD
[1.2.1]: https://github.com/brlanweb/go-stock/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/brlanweb/go-stock/compare/v1.1.1...v1.2.0
[1.1.1]: https://github.com/brlanweb/go-stock/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/brlanweb/go-stock/releases/tag/v1.1.0
