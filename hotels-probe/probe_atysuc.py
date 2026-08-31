#!/usr/bin/env python3
"""Reference prober for the anonymous AtySUc hotel search call.

Used to (a) confirm anonymous access with a null search token, (b) determine the
[Y,M,D] month indexing, and (c) extract hotel name+price from the nested
stringified-protobuf response. This is the reference the Go parser mirrors.

    python3 probe_atysuc.py --feature "0x89c24fa5d33f083b:0xc80b8f06e177fe62" \
        --place "New York" --checkin 2026-06-01 --checkout 2026-06-11
"""
import argparse
import datetime as dt
import json
import re
import urllib.parse
import urllib.request

UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/113.0.0.0 Safari/537.36"
BL = "boq_travel-frontend-ui_20260515.02_p0"
FSID = "2210727468203238887"


def anon_cookies() -> str:
    req = urllib.request.Request(
        "https://www.google.com/travel/search?q=hotels+in+New+York",
        headers={"User-Agent": UA},
        method="HEAD",
    )
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            sc = r.headers.get_all("Set-Cookie") or []
    except Exception:
        sc = []
    return ";".join(c.split(";", 1)[0] for c in sc)


def build_inner(query, feature, place, checkin: dt.date, checkout: dt.date, month_index_base, currency, stars):
    # date triple uses [year, month, day]; month_index_base lets us test 0 vs 1
    ci = [checkin.year, checkin.month - (1 - month_index_base), checkin.day]
    co = [checkout.year, checkout.month - (1 - month_index_base), checkout.day]
    nights = (checkout - checkin).days
    star_slot = [[[stars], [stars]], 0] if stars else [[[], []], 0]
    return [
        query,
        [
            1,
            star_slot,
            [
                [None, [[None, None, None, None, None, feature, place]]],
                [None, [ci, co, 1], None, None, None, [None, 0]],
            ],
            None,
            [[None, None, None, None, None, None, currency]],
        ],
        [None, None, None, None, None, None, 13],  # null search token
        None,
        1,
    ]


def call(inner, cookies):
    freq = [[["AtySUc", json.dumps(inner, separators=(",", ":")), None, "1"]]]
    body = "f.req=" + urllib.parse.quote(json.dumps(freq, separators=(",", ":"))) + "&at=&"
    url = (
        "https://www.google.com/_/TravelFrontendUi/data/batchexecute"
        f"?rpcids=AtySUc&source-path=%2Ftravel%2Fsearch&f.sid={FSID}&bl={BL}"
        "&hl=en&soc-app=162&soc-platform=1&soc-device=1&_reqid=2181955&rt=c"
    )
    headers = {
        "User-Agent": UA,
        "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8",
        "X-Same-Domain": "1",
        "Origin": "https://www.google.com",
        "x-goog-ext-259736195-jspb": '["en-US","US","USD",2,null,[240],null,null,7,[]]',
    }
    if cookies:
        headers["Cookie"] = cookies
    req = urllib.request.Request(url, data=body.encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read().decode("utf-8", "replace")


def extract_payload(raw):
    raw = raw.split("\n", 1)[1] if raw.startswith(")]}'") else raw
    decd = json.JSONDecoder()
    i, arrs = 0, []
    n = len(raw)
    while i < n:
        while i < n and raw[i].isspace():
            i += 1
        j = i
        while j < n and raw[j].isdigit():
            j += 1
        if j > i and j < n and raw[j] in "\r\n":
            i = j
            while i < n and raw[i].isspace():
                i += 1
        if i >= n or raw[i] != "[":
            break
        o, e = decd.raw_decode(raw, i)
        arrs.append(o)
        i = e
    out = []
    def fa(x):
        if isinstance(x, list):
            if len(x) >= 3 and x[0] == "wrb.fr" and x[1] == "AtySUc" and isinstance(x[2], str):
                out.append(x[2])
            for e in x:
                fa(e)
    for a in arrs:
        fa(a)
    return out[0] if out else None


def deep(x, depth=0):
    if depth > 14:
        return x
    if isinstance(x, str):
        s = x.strip()
        if len(s) > 1 and s[0] in "[{" and s[-1] in "]}":
            try:
                return deep(json.loads(s), depth + 1)
            except Exception:
                return x
        return x
    if isinstance(x, list):
        return [deep(e, depth + 1) for e in x]
    if isinstance(x, dict):
        return {k: deep(v, depth + 1) for k, v in x.items()}
    return x


def hotels_from(deep_obj):
    """Field 179305178 maps hold per-hotel records: [null, name, [[lat,lng]...], ...].
    Prices show up as '$N' strings nearby. We collect (name, min $price) heuristically."""
    rows = {}
    def visit(x):
        if isinstance(x, dict):
            for k, v in x.items():
                if k == "179305178" and isinstance(v, list):
                    name = next((e for e in v if isinstance(e, str) and len(e) > 3), None)
                    dollars = re.findall(r"\$([0-9][0-9,]*)", json.dumps(v))
                    price = min((int(d.replace(",", "")) for d in dollars), default=None)
                    if name:
                        if name not in rows or (price and (rows[name] is None or price < rows[name])):
                            rows[name] = price
                visit(v)
        elif isinstance(x, list):
            for e in x:
                visit(e)
    visit(deep_obj)
    return rows


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--feature", default="0x89c24fa5d33f083b:0xc80b8f06e177fe62")
    ap.add_argument("--place", default="New York")
    ap.add_argument("--query", default=None)
    ap.add_argument("--checkin", required=True)
    ap.add_argument("--checkout", required=True)
    ap.add_argument("--month-base", type=int, default=1, choices=[0, 1],
                    help="1: month as-is (1-indexed); 0: subtract 1 (JS 0-indexed)")
    ap.add_argument("--stars", type=int, default=0, help="0=no filter, else star class")
    ap.add_argument("--anon", action="store_true", help="use fresh anon cookies")
    args = ap.parse_args()

    ci = dt.date.fromisoformat(args.checkin)
    co = dt.date.fromisoformat(args.checkout)
    query = args.query or f"hotels in {args.place}"
    cookies = anon_cookies() if args.anon else ""
    inner = build_inner(query, args.feature, args.place, ci, co, args.month_base, "USD", args.stars)
    raw = call(inner, cookies)
    pl = extract_payload(raw)
    if not pl:
        print("no AtySUc payload"); return
    d = deep(json.loads(pl))
    rows = hotels_from(d)
    print(f"checkin={ci} checkout={co} month_base={args.month_base} stars={args.stars} -> {len(rows)} hotels")
    for name, price in sorted(rows.items(), key=lambda kv: (kv[1] is None, kv[1] or 0))[:15]:
        print(f"   ${price if price is not None else '--':>6}  {name[:55]}")


if __name__ == "__main__":
    main()
