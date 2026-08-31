#!/usr/bin/env python3
"""Proof-of-concept hotel "price graph": sweep check-in dates over a window and
report the cheapest stay of a fixed duration, optionally filtered by star class.

This mirrors the flights price-graph idea but is built on HTML scraping of
Google's immersive hotel-search page (the priced listing is server-rendered into
the DOM; there is no clean batchexecute JSON for it).

Example — cheapest 10-night NYC stay departing in June 2026, 4-5 star only:

    python3 hotel_pricegraph.py \
        --place "New York" \
        --start 2026-06-01 --end 2026-06-20 \
        --nights 10 --min-stars 4 --step 2

NOTE / CAVEAT: the scraped price appears to be the nightly "from" rate, not the
stay total. Verify before trusting absolute numbers; it is reliable for *ranking*
date windows against each other, which is what this POC demonstrates.
"""
import argparse
import datetime as dt
import sys
import time
import urllib.parse
import urllib.request

from parse_hotels import parse

UA = (
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 "
    "(KHTML, like Gecko) Chrome/113.0.0.0 Safari/537.36"
)


def fetch(place: str, checkin: str, checkout: str) -> str:
    q = urllib.parse.urlencode(
        {"q": f"hotels in {place}", "checkin": checkin, "checkout": checkout}
    )
    url = f"https://www.google.com/travel/search?{q}"
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read().decode("utf-8", "replace")


def daterange(start: dt.date, end: dt.date, step: int):
    d = start
    while d <= end:
        yield d
        d += dt.timedelta(days=step)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--place", required=True)
    ap.add_argument("--start", required=True, help="earliest check-in YYYY-MM-DD")
    ap.add_argument("--end", required=True, help="latest check-in YYYY-MM-DD")
    ap.add_argument("--nights", type=int, default=10)
    ap.add_argument("--min-stars", type=float, default=0.0)
    ap.add_argument("--max-stars", type=float, default=5.0)
    ap.add_argument("--step", type=int, default=1, help="days between sampled check-ins")
    ap.add_argument("--sleep", type=float, default=1.5, help="seconds between requests")
    args = ap.parse_args()

    start = dt.date.fromisoformat(args.start)
    end = dt.date.fromisoformat(args.end)

    results = []
    for ci in daterange(start, end, args.step):
        co = ci + dt.timedelta(days=args.nights)
        try:
            html = fetch(args.place, ci.isoformat(), co.isoformat())
        except Exception as e:
            print(f"{ci}  fetch error: {e}", file=sys.stderr)
            continue
        hotels = parse(html)
        # apply star filter (drop unknown-star rows only when a filter is set)
        filt = [
            h
            for h in hotels
            if h["stars"] is not None
            and args.min_stars <= h["stars"] <= args.max_stars
        ]
        pool = filt if args.min_stars > 0 else hotels
        if not pool:
            print(f"{ci} -> {co}  no matching hotels", file=sys.stderr)
            time.sleep(args.sleep)
            continue
        cheapest = min(pool, key=lambda h: h["price"])
        results.append((ci, co, cheapest, len(pool)))
        print(
            f"{ci} -> {co}  cheapest ${cheapest['price']:<5} "
            f"{(str(cheapest['stars'])+'*') if cheapest['stars'] else '':>4}  "
            f"{cheapest['name'][:45]:<45}  ({len(pool)} hotels)"
        )
        time.sleep(args.sleep)

    if results:
        best = min(results, key=lambda r: r[2]["price"])
        ci, co, h, _ = best
        print(
            f"\nCHEAPEST WINDOW: {ci} -> {co}  ${h['price']}  "
            f"{(str(h['stars'])+'*') if h['stars'] else ''}  {h['name']}"
        )


if __name__ == "__main__":
    main()
