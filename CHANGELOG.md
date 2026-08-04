# Changelog

All notable changes to this project are documented in this file.

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

[1.1.0]: https://github.com/brlanweb/go-stock/releases/tag/v1.1.0
