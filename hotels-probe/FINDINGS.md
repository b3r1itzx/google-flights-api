# Google Hotels feasibility probe

Question: can we add a hotels feature to this library that works like the flights
side ("cheapest N-night stay in <place> next month, 4-5 star")?

Short answer: **yes, but via a different mechanism than flights.** Flights uses a
clean `batchexecute` JSON RPC. Hotels' priced listing is **server-rendered HTML**;
the JSON RPC only does location resolution.

## What was confirmed

1. **Same transport exists.** Google Hotels uses the identical `batchexecute`
   endpoint, on app `boq_travel-frontend-ui` (note: `TravelFrontendUi`, not
   `FlightsFrontendUi`). Build label (`cfb2h`) and session id (`FdrFJe`) are
   scrapeable from the page exactly like flights. A POST returns HTTP 200.

2. **RPC `AtySUc` = geo resolution only.** Payload `["<query>",[],[],[]]`. For
   `"hotels in New York"` it returns the place name + a Maps feature ID
   (`0x89c24fa5d33f083b:0xc80b8f06e177fe62`) — no hotel list, no prices.

3. **The priced hotel list is server-rendered into the search-page DOM**, not in
   any init `batchexecute` JSON (`ds:0`/`ds:1` are `AtySUc`/`c8GpOe`, both config).
   The live listing fires as a post-load XHR we could not capture here (headless
   Chromium is killed by this sandbox), but we don't need it — the static HTML
   already contains names, star class, and prices.

4. **`checkin` / `checkout` URL params drive date-specific prices.** Verified:
   June vs September render different price distributions and each echoes its own
   dates. URL form:
   `https://www.google.com/travel/search?q=hotels in <place>&checkin=YYYY-MM-DD&checkout=YYYY-MM-DD`

5. **Parse signals** (see `parse_hotels.py`):
   - name + price: `aria-label="Prices starting from $<n>, <name>"`
   - star class: a nearby span `...·<n>-star hotel`

## Scripts in this folder

- `parse_hotels.py` — extract `(name, stars, price)` from a saved/streamed search page.
- `hotel_pricegraph.py` — sweep check-in dates over a window, filter by star class,
  report the cheapest stay. End-to-end POC.
- `capture.mjs` — Chromium DevTools capture of the post-load hotel XHR. Did NOT run
  here (sandbox kills Chromium, signal 16). Keep for running on a real machine if we
  ever want the JSON RPC route instead of HTML scraping.

POC run (`--place "New York" --start 2026-06-01 --end 2026-06-13 --nights 10 --min-stars 4 --step 3`)
returned a cheapest 4★ option per window successfully.

## Price semantics — RESOLVED (important)

Tested by holding hotel + check-in fixed and varying nights, then holding nights
fixed and varying dates across seasons:

- **The static-HTML price is PER NIGHT, not a stay total.** A hotel shows the same
  figure for a 1-, 5-, or 10-night stay.
- **The static-HTML price is a representative/catalog rate that does NOT vary by
  date.** 0 of 14 common hotels changed price between New Year's Eve and February;
  the Marriott Marquis (Times Square) showed $311/night even on Dec 31 — impossible
  as a live rate. Earlier "June vs September differ" was different *hotels* in the
  list, not the same hotel repricing.

**Consequence:** the static-HTML scrape returns the same price for every date.
The live, date-specific rates come from a post-load XHR — which we have now
captured and replayed (below).

## Live pricing XHR — CAPTURED & REPLAYED (anonymous works!)

Captured via browser DevTools (manual "copy as cURL"). Two RPCs on the same
`TravelFrontendUi/data/batchexecute` transport flights uses:

- **`AtySUc`** — search/listing. Inner request schema (decoded):
  ```
  [ "<query string>",
    [ 1,
      [[[3],[3]],0],                         # filter slot (star/sort — TBD)
      [ [ null, [[null,null,null,null,null,"<featureId 0x..:0x..>","<place>"]] ],
        [ null, [[Y,M,D],[Y,M,D],<n>], null,null,null,[null,0] ] ],   # check-in, check-out, dates
      null,
      [[null,null,null,null,null,null,"USD"]] ],                      # currency
    [null,null,null,null,null,"<search token>",13], null, 1 ]
  ```
  The feature ID comes from the geo-resolve step (same `0x89c2...` we already
  saw). Dates are `[year,month,day]` triples (0-vs-1-indexed month still TBD).
- **`ulMuIc`** — follow-up: takes hotel cluster-ID pairs
  (`["9926976607132534401","13120119310674950160"]`) for batch price/detail.

**KEY FINDING: `AtySUc` works fully ANONYMOUSLY** — replayed with only fresh
anonymous cookies, empty `at=`, and NO `x-goog-batchexecute-bgr` anti-abuse
token. HTTP 200, and the response contains hotel names + prices
(`$39, $207, $247, $375 ...` plus stay-totals). So hotels can be a pure
anonymous-HTTP feature exactly like flights — no browser at runtime, no login.

**Response shape:** Google's nested "stringified-protobuf" — hotel records live
in double-encoded JSON strings keyed by numeric field IDs (`179305178`,
`404340221`, `415404532`, `441552390`, `449069993`). A parser must recursively
`json.loads` string values that are themselves JSON, then pull name/price/stars
out of the field-keyed maps.

`capture.mjs` (browser automation) is now only a convenience for re-capturing if
the schema changes; it is NOT needed for the runtime feature.

The static-HTML scrape (`parse_hotels.py`) remains a fallback for listing hotels
+ star class + catalog price without any reverse-engineering.

## Other caveats
- **Pagination:** only ~18 hotels render per page (~6-7 are 4★+). Need to page for
  full coverage / a true min.
- **Fragility:** HTML scraping breaks on DOM/class changes. The `aria-label`
  anchors are more stable than CSS classes, but still riskier than the flights JSON.
- **Star filter via URL:** we filtered client-side. Google supports star/price
  filters encoded in the (protobuf) `ts`/`qs` URL param — worth reverse-engineering
  so the server does the filtering and pagination is smaller.
- **No timezone/geo handling, no proxy/cookie reuse** wired in yet (the flights
  `Session` already has these — reuse it).

## Recommended path if we proceed

Build a `hotels` package parallel to `flights`:
- Reuse `Session` (cookies, build-label/sid fetch, retry client).
- `GetHotelOffers(place, checkin, checkout, opts)` → fetch the search HTML, parse
  with the aria-label anchors (port `parse_hotels.py` to Go), return structs.
- `GetHotelPriceGraph(...)` → date sweep over `GetHotelOffers`, mirroring the
  flights price-graph API and the `gflights` CLI shape.
- Resolve nightly-vs-total and URL-encoded filters first; both affect the API shape.
