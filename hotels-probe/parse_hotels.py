#!/usr/bin/env python3
"""Extract (name, stars, price) hotel tuples from a Google Hotels search page.

Google server-renders the hotel list into the immersive search page DOM. The
price + name come from an aria-label:

    aria-label="Prices starting from $247, <hotel name>"

The star class renders as a nearby span within the same card:

    ...&middot;&nbsp;</span>4-star hotel</span>

We anchor on each price label and look ahead within the same card for the
star span. Usage:

    python3 parse_hotels.py <saved-search.html>
    curl -s ... | python3 parse_hotels.py -
"""
import html as htmllib
import re
import sys


PRICE_RE = re.compile(r'aria-label="Prices starting from \$([0-9,]+), ([^"]+?)"')
STAR_AHEAD_RE = re.compile(r'([0-9](?:\.[0-9])?)-star hotel')
# Trailing marketing noise Google appends to names in the aria-label.
NOISE_RE = re.compile(
    r"\s*(?:GREAT DEAL|DEAL)\s+\d+%?\s*(?:less than usual)?\s*$", re.I
)


def clean_name(raw: str) -> str:
    name = htmllib.unescape(raw).strip()
    # Drop per-room-type suffix ("Hotel X - Classic Quadruple Room").
    name = name.split(" - ")[0].strip()
    name = NOISE_RE.sub("", name).strip()
    return name


def parse(html_text: str):
    matches = list(PRICE_RE.finditer(html_text))
    best = {}  # name -> {price, stars}
    for idx, m in enumerate(matches):
        price = int(m.group(1).replace(",", ""))
        name = clean_name(m.group(2))
        if not name:
            continue
        # Look ahead only as far as the next price card to keep the star
        # association within this hotel's block.
        end = matches[idx + 1].start() if idx + 1 < len(matches) else m.end() + 2500
        window = html_text[m.end():end]
        sm = STAR_AHEAD_RE.search(window)
        stars = float(sm.group(1)) if sm else None

        # Same hotel can repeat per room-type; keep the lowest price and any
        # star rating we manage to find.
        cur = best.get(name)
        if cur is None or price < cur["price"]:
            best[name] = {"price": price, "stars": stars if stars is not None else (cur or {}).get("stars")}
        elif stars is not None and cur.get("stars") is None:
            cur["stars"] = stars

    rows = [{"name": n, "price": v["price"], "stars": v["stars"]} for n, v in best.items()]
    rows.sort(key=lambda r: r["price"])
    return rows


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else "-"
    html = sys.stdin.read() if path == "-" else open(path, encoding="utf-8").read()
    rows = parse(html)
    print(f"parsed {len(rows)} hotels")
    for r in rows[:30]:
        star = f"{r['stars']}*" if r["stars"] is not None else "  ?"
        print(f"  ${r['price']:>5}  {star:>4}  {r['name'][:60]}")


if __name__ == "__main__":
    main()
