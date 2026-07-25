#!/usr/bin/env python3
import json
import math
import sys
from contextlib import redirect_stdout
from datetime import datetime
from io import StringIO


def emit(payload):
    json.dump(payload, sys.stdout, ensure_ascii=False, separators=(",", ":"), allow_nan=False)


def clean_number(value, default=0.0):
    try:
        number = float(value)
        return number if math.isfinite(number) else default
    except (TypeError, ValueError):
        return default


def date_arg(value, default):
    if not value or value == "0":
        return default
    return value.replace("-", "")


def provider_symbol(symbol):
    market = symbol[:2].upper()
    code = symbol[2:]
    if market not in {"SH", "SZ", "BJ"} or not code.isdigit():
        raise ValueError(f"unsupported symbol: {symbol}")
    return market, code


def normalize_rows(symbol, rows):
    result = []
    previous_close = 0.0
    for row in sorted(rows, key=lambda item: item["date"]):
        close = clean_number(row.get("close"))
        preclose = clean_number(row.get("preclose"), previous_close)
        change_pct = (close - preclose) / preclose * 100 if preclose > 0 else 0.0
        result.append({
            "symbol": symbol,
            "date": row["date"],
            "open": clean_number(row.get("open")),
            "high": clean_number(row.get("high")),
            "low": clean_number(row.get("low")),
            "close": close,
            "volume": int(clean_number(row.get("volume"))),
            "amount": clean_number(row.get("amount")),
            "change_pct": change_pct,
            "turnover_rate": clean_number(row.get("turnover_rate")),
            "adj_factor": clean_number(row.get("adj_factor"), 1.0),
        })
        previous_close = close
    return result


def fetch_baostock(symbol, beg, end):
    import baostock as bs

    market, code = provider_symbol(symbol)
    if market == "BJ":
        raise ValueError("baostock does not support Beijing Stock Exchange symbols")
    with redirect_stdout(StringIO()):
        login = bs.login()
    if login.error_code != "0":
        raise RuntimeError(f"baostock login: {login.error_code} {login.error_msg}")
    try:
        rs = bs.query_history_k_data_plus(
            f"{market.lower()}.{code}",
            "date,open,high,low,close,preclose,volume,amount,turn,pctChg,adjustflag",
            start_date=datetime.strptime(date_arg(beg, "19900101"), "%Y%m%d").strftime("%Y-%m-%d"),
            end_date=datetime.strptime(date_arg(end, datetime.now().strftime("%Y%m%d")), "%Y%m%d").strftime("%Y-%m-%d"),
            frequency="d",
            adjustflag="3",
        )
        if rs.error_code != "0":
            raise RuntimeError(f"baostock query: {rs.error_code} {rs.error_msg}")
        rows = []
        while rs.next():
            values = dict(zip(rs.fields, rs.get_row_data()))
            rows.append({
                "date": values["date"],
                "open": values["open"], "high": values["high"],
                "low": values["low"], "close": values["close"],
                "preclose": values["preclose"], "volume": values["volume"],
                "amount": values["amount"], "turnover_rate": clean_number(values["turn"]),
                "adj_factor": 1,
            })
        return normalize_rows(symbol, rows)
    finally:
        with redirect_stdout(StringIO()):
            bs.logout()


def ak_frame(symbol, beg, end, adjust=""):
    import akshare as ak
    market, code = provider_symbol(symbol)
    start = date_arg(beg, "19900101")
    finish = date_arg(end, datetime.now().strftime("%Y%m%d"))
    if market in {"SH", "SZ"} and (code.startswith(("15", "16", "18", "51", "56", "58"))):
        frame = ak.fund_etf_hist_sina(symbol=f"{market.lower()}{code}")
        frame = frame[(frame["date"].astype(str).str.replace("-", "") >= start) & (frame["date"].astype(str).str.replace("-", "") <= finish)]
        return frame
    return ak.stock_zh_a_daily(symbol=f"{market.lower()}{code}", start_date=start, end_date=finish, adjust=adjust)


def fetch_akshare(symbol, beg, end):
    raw = ak_frame(symbol, beg, end, "")
    if raw is None or raw.empty:
        return []
    try:
        adjusted = ak_frame(symbol, beg, end, "qfq")
        qfq_close = {str(row["date"]): clean_number(row["close"]) for _, row in adjusted.iterrows()}
    except Exception:
        qfq_close = {}
    rows = []
    for _, item in raw.iterrows():
        date = str(item["date"])
        close = clean_number(item.get("close"))
        turnover = clean_number(item.get("turnover"))
        rows.append({
            "date": date,
            "open": item.get("open"), "high": item.get("high"),
            "low": item.get("low"), "close": close,
            "volume": item.get("volume"), "amount": item.get("amount"),
            "turnover_rate": turnover * 100 if abs(turnover) <= 1 else turnover,
            "adj_factor": qfq_close.get(date, close) / close if close > 0 else 1,
        })
    return normalize_rows(symbol, rows)


def main():
    if len(sys.argv) != 5:
        raise ValueError("usage: fetch_kline.py <baostock|akshare> <symbol> <beg> <end>")
    source, symbol, beg, end = sys.argv[1:]
    if source == "baostock":
        rows = fetch_baostock(symbol, beg, end)
    elif source == "akshare":
        rows = fetch_akshare(symbol, beg, end)
    else:
        raise ValueError(f"unknown source: {source}")
    emit({"source": source, "klines": rows})


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        emit({"error": str(exc)})
        sys.exit(1)
