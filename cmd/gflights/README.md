# gflights

`gflights` is a thin CLI wrapper around the
[google-flights-api](https://github.com/b3r1itzx/google-flights-api) Go library.
It queries Google Flights for cheapest-fare-per-date price graphs and detailed
flight offers. It supports plain-text output for humans and a stable JSON shape
(`--json`) intended for agent consumption.

> The underlying library scrapes a non-public Google endpoint. It can break at
> any time when Google changes the backend. Treat results as best-effort.

## Install

### `go install` (recommended)

Requires Go 1.20+.

```sh
go install github.com/b3r1itzx/google-flights-api/cmd/gflights@latest
```

The binary lands in `$(go env GOBIN)` (or `$(go env GOPATH)/bin` if `GOBIN` is
unset). Make sure that directory is on your `PATH`:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```

Verify:

```sh
gflights --help
```

### Build from source

```sh
git clone https://github.com/b3r1itzx/google-flights-api.git
cd google-flights-api
go build -o gflights ./cmd/gflights
./gflights --help
```

To install the local build somewhere on `PATH`:

```sh
sudo install -m 0755 gflights /usr/local/bin/gflights
```

## Subcommands

```
gflights pricegraph        cheapest round-trip price per departure date over a date range
gflights offers            detailed flight offers for a specific departure (+ return) date
gflights deals             cheapest destinations from an origin, ranked (fan-out explore)
gflights hotels            hotel offers for a location and stay dates
gflights hotel-pricegraph  cheapest hotel per check-in date over a range (fixed nights)
```

Run `gflights <subcommand> --help` for the full flag list.

### Common flags

| Flag             | Default       | Notes                                                                  |
|------------------|---------------|------------------------------------------------------------------------|
| `--from`         | _required_    | City name or IATA code. Comma-separated for multiple (e.g. `JFK,EWR`). |
| `--to`           | _required_    | City name or IATA code. Same comma rule.                               |
| `--adults`       | `1`           |                                                                        |
| `--children`     | `0`           |                                                                        |
| `--infants-lap`  | `0`           |                                                                        |
| `--infants-seat` | `0`           |                                                                        |
| `--class`        | `economy`     | `economy` \| `premium-economy` \| `business` \| `first`                |
| `--stops`        | `any`         | `any` \| `nonstop` \| `stop1` \| `stop2`                               |
| `--trip-type`    | `round-trip`  | `round-trip` \| `one-way`                                              |
| `--currency`     | `USD`         | ISO 4217 code                                                          |
| `--lang`         | `en`          | BCP 47 tag (used for city-name resolution)                             |
| `--json`         | off           | Emit a JSON document on stdout instead of a human table.               |

IATA detection: any 3-uppercase-letter token (e.g. `JFK`, `FCO`) is treated as
an airport code; anything else is treated as a city name.

### `pricegraph`

Extra flags:

| Flag         | Default | Notes                                                            |
|--------------|---------|------------------------------------------------------------------|
| `--start`    | _req._  | `YYYY-MM-DD`. Range start (must be ≥ today).                     |
| `--end`      | _req._  | `YYYY-MM-DD`. Within 161 days of `--start`.                      |
| `--duration` | `7`     | Trip length in days.                                             |
| `--sort`     | `date`  | `date` \| `price`.                                               |

Example:

```sh
gflights pricegraph \
  --from "New York" --to Rome \
  --start 2026-07-01 --end 2026-07-31 \
  --duration 15
```

### `offers`

Extra flags:

| Flag        | Default | Notes                                                            |
|-------------|---------|------------------------------------------------------------------|
| `--depart`  | _req._  | `YYYY-MM-DD`.                                                    |
| `--return`  | -       | `YYYY-MM-DD`. Required unless `--trip-type one-way`.             |
| `--limit`   | `20`    | Max offers to print (`0` = no limit).                            |
| `--sort`    | `price` | `price` \| `duration` \| `departure`.                            |
| `--url`     | `true`  | Include the Google Flights deep-link URL in the output.          |

Example:

```sh
gflights offers \
  --from JFK --to FCO \
  --depart 2026-07-06 --return 2026-07-21 \
  --limit 5
```

One-way:

```sh
gflights offers --from JFK --to FCO --depart 2026-07-06 --trip-type one-way
```

### `deals`

Finds the cheapest destinations from one origin — the origin-wide "explore"
the underlying API has no single call for. It runs one `pricegraph` search per
destination (in parallel) and reports the cheapest round-trip per destination.
Destinations come from `--to` (comma-separated), `--preset`, or both.

Each destination also gets a **deal score**: how far (percent) its cheapest
fare sits below the route's own *typical* fare — the median across the searched
range. That's what separates "cheap route" from "unusually cheap right now,"
which is what an impulse-deal alert wants to surface.

Extra flags (plus all the common flags; `--to` is a comma-separated **list**
here, and `--from` is the only required route flag):

| Flag             | Default | Notes                                                                 |
|------------------|---------|-----------------------------------------------------------------------|
| `--start`        | _req._  | `YYYY-MM-DD`. Range start (≥ today).                                  |
| `--end`          | _req._  | `YYYY-MM-DD`. Within 161 days of `--start`.                           |
| `--duration`     | `7`     | Trip length in days.                                                  |
| `--preset`       | -       | Built-in destination set(s), comma-separated. See table below.        |
| `--sort`         | `price` | `price` (cheapest first) or `deal` (biggest % below typical first).   |
| `--min-discount` | `0`     | Only show destinations at least this percent below their typical fare.|
| `--concurrency`  | `4`     | Destinations searched in parallel.                                    |
| `--limit`        | `0`     | Show only the top N after sorting (`0` = all).                        |

Built-in presets (combine with commas, e.g. `--preset caribbean,europe`):

| Preset      | Contents                                                       |
|-------------|----------------------------------------------------------------|
| `us-major`  | ~30 busiest US airports.                                       |
| `caribbean` | 12 popular Caribbean leisure spots (SJU, PUJ, MBJ, NAS, AUA, …). |
| `europe`    | 12 popular European destinations (LHR, CDG, FCO, BCN, MAD, …).  |

The origin is removed from the destination list automatically, and duplicates
are de-duped (case-insensitive). Each destination costs one browser search
(~6–8s) but they run `--concurrency` at a time, so 30 destinations at
`--concurrency 6` finishes in ~20–45s.

```sh
# Cheapest destinations from Norfolk across a set of cities
gflights deals --from ORF --to MCO,ATL,LAS,DEN,BOS \
  --start 2026-11-01 --end 2026-11-30 --duration 4

# Impulse-deal feed across US + beach + Europe: fares 25%+ below typical,
# biggest drop first
gflights deals --from ORF --preset us-major,caribbean,europe \
  --start 2026-11-01 --end 2027-01-31 --duration 7 \
  --sort deal --min-discount 25 --json
```

The `TYPICAL` column (median fare over the range) and `DEAL` column (percent
below typical) accompany the cheapest price. **Cheapest ≠ best deal**: a route
that's always cheap scores a low discount, while a normally-expensive route on
an unusual dip scores high — rank by `--sort deal` for the latter.

Destinations with no priced offer in the range are dropped from the ranking and
listed separately (text) / under `failures` (JSON). The upstream calendar RPC
occasionally returns empty for a destination — more so under a busy parallel
sweep — so `deals` automatically retries the empties once at low concurrency
before reporting them as failures. In practice that recovers essentially all
transient drops (a 24-destination international sweep goes from ~half empty to
complete).

### Hotel common flags (`hotels`, `hotel-pricegraph`)

| Flag          | Default    | Notes                                                                  |
|---------------|------------|------------------------------------------------------------------------|
| `--location`  | _required_ | City, place, ZIP — or a hotel name to search around (see `--name`).    |
| `--name`      | -          | Only include hotels whose name contains this (case-insensitive).       |
| `--min-stars` | `0`        | Minimum hotel class 1–5 (`0` = no minimum). Applied client-side.       |
| `--max-stars` | `0`        | Maximum hotel class 1–5 (`0` = no maximum).                            |
| `--currency`  | `USD`      | ISO 4217 code. Encoded into the page URL, so it works from any region. |
| `--lang`      | `en`       | BCP 47 tag.                                                            |
| `--json`      | off        | Emit JSON instead of a human table.                                    |

**Tracking one specific hotel**: pass the hotel's name (plus city) as
`--location` — Google then returns it as an "entity match" alongside the area
listing — and pass a distinctive fragment of its name as `--name` to filter
everything else out:

```sh
gflights hotels --location "Enchantment Resort Sedona" --name Enchantment \
  --checkin 2026-12-12 --checkout 2026-12-17
```

### `hotels`

Extra flags:

| Flag         | Default | Notes                                            |
|--------------|---------|--------------------------------------------------|
| `--checkin`  | _req._  | `YYYY-MM-DD` (must be ≥ today).                  |
| `--checkout` | _req._  | `YYYY-MM-DD` (after `--checkin`).                |
| `--limit`    | `20`    | Max hotels to print (`0` = no limit).            |
| `--sort`     | `price` | `price` \| `stars` \| `rating`.                  |

Prices are **nightly** rates for the requested stay. Hotels with no available
price for the window show `--` (text) / `"price": 0` (JSON) and sort last.

```sh
gflights hotels --location "New York" --checkin 2026-06-01 --checkout 2026-06-11 \
  --min-stars 4 --max-stars 5 --sort rating
```

### `hotel-pricegraph`

Sweeps check-in dates across a range (one search per sampled date), keeping the
cheapest hotel that passes the star/name filters for each window. This answers
"when is the cheapest N-night stay?" — for a whole destination, or for one
hotel when combined with `--name`.

Extra flags:

| Flag       | Default | Notes                                                      |
|------------|---------|------------------------------------------------------------|
| `--start`  | _req._  | `YYYY-MM-DD`. Earliest check-in to sample.                 |
| `--end`    | _req._  | `YYYY-MM-DD`. Latest check-in to sample.                   |
| `--nights` | `7`     | Length of stay.                                            |
| `--step`   | `1`     | Days between sampled check-ins (raise it for wide ranges). |

Output includes both the per-night rate and the stay total per date, plus a
final `CHEAPEST:` line. Dates with no qualifying priced hotel are omitted — a
missing row means sold out / no offer, not zero.

```sh
# Cheapest 5-night stay at one hotel across December
gflights hotel-pricegraph --location "Enchantment Resort Sedona" --name Enchantment \
  --start 2026-12-01 --end 2026-12-26 --nights 5

# Cheapest 4-5 star, 10-night stay anywhere in New York, sampling every 2nd day
gflights hotel-pricegraph --location "New York" --min-stars 4 \
  --start 2026-06-01 --end 2026-06-30 --nights 10 --step 2
```

Each sampled date costs one browser page load (~3–5s), so a 30-day range at
`--step 1` runs ~2 minutes.

### Hotel caveats

- **Vendor coverage follows the machine's market.** Google shows different
  booking vendors (and therefore different lowest prices) depending on the IP's
  country and sign-in state. The CLI returns the cheapest offer *its* session
  is served; run it from the market you care about.
- The area listing carries ~18 hotels per search, so a wide-open city query is
  a sample, not an exhaustive minimum. Entity-match queries (`--name`) are not
  affected by this.

## JSON shape (for agents)

`--json` emits a single JSON object on stdout. Errors go to stderr and the
process exits non-zero. The shape is stable enough to parse with `jq`.

### `pricegraph --json`

```json
{
  "type": "pricegraph",
  "query": { /* echoes the CLI flags */ },
  "count": 31,
  "offers": [
    { "depart": "2026-07-06", "return": "2026-07-21", "price": 698, "currency": "USD" }
  ]
}
```

### `offers --json`

```json
{
  "type": "offers",
  "query": { /* echoes the CLI flags */ },
  "price_range": { "low": 500, "high": 700 },
  "url": "https://www.google.com/travel/flights/search?tfs=...",
  "count": 42,
  "offers": [
    {
      "price": 698,
      "currency": "USD",
      "duration_minutes": 480,
      "stops": 0,
      "src_airport": "JFK",
      "dst_airport": "FCO",
      "src_city": "New York",
      "dst_city": "Rome",
      "flights": [
        {
          "airline": "Norse Atlantic Airways",
          "flight_number": "N0 402",
          "from": "JFK",
          "from_name": "John F. Kennedy International Airport",
          "from_city": "New York",
          "to": "FCO",
          "to_name": "Leonardo da Vinci International Airport",
          "to_city": "Rome",
          "departure": "2026-07-06T00:30:00-04:00",
          "arrival": "2026-07-06T14:30:00+02:00",
          "duration_minutes": 480,
          "airplane": "Boeing 787",
          "legroom": "31 inches"
        }
      ]
    }
  ]
}
```

`price_range` is optional (only present when Google returns a typical-range
hint). `url` is optional (omitted if URL serialization failed).

### `deals --json`

```json
{
  "type": "deals",
  "query": { "from": "ORF", "to": "MCO,ATL,LAS", "range_start": "2026-11-01", "range_end": "2026-11-30", "duration_days": 4, "...": "..." },
  "destinations_searched": 3,
  "total_with_offers": 3,
  "count": 3,
  "deals": [
    { "dest": "ATL", "price": 80, "typical": 160, "discount": 50, "depart": "2026-11-03", "return": "2026-11-07", "currency": "USD" }
  ],
  "failures": [
    { "dest": "SomePlace", "reason": "no offers with a price" }
  ]
}
```

`deals` is ordered by `--sort` (cheapest first, or biggest `discount` first);
`count` is how many are shown (after `--min-discount` and `--limit`),
`total_with_offers` how many had any offer. Each entry carries `typical` (median
fare) and `discount` (percent below typical). The single best deal is
`.deals[0]`.

### `hotels --json`

```json
{
  "type": "hotels",
  "query": {
    "location": "New York", "name": "", "checkin": "2026-06-01",
    "checkout": "2026-06-11", "nights": 10,
    "min_stars": 4, "currency": "USD", "lang": "en"
  },
  "count": 7,
  "hotels": [
    {
      "name": "Aura Hotel Times Square",
      "price": 213.4,
      "currency": "USD",
      "stars": 4,
      "rating": 4.3,
      "review_count": 1289,
      "latitude": 40.76,
      "longitude": -73.98,
      "id": "11419601027756791404"
    }
  ]
}
```

`price` is the nightly rate (`0` = no price available for the window).
`base_price` appears when Google shows a strikethrough "usual" rate.

### `hotel-pricegraph --json`

```json
{
  "type": "hotel-pricegraph",
  "query": {
    "location": "Enchantment Resort Sedona", "name": "Enchantment",
    "nights": 5, "currency": "USD", "lang": "en"
  },
  "count": 18,
  "offers": [
    {
      "checkin": "2026-12-12",
      "checkout": "2026-12-17",
      "stay_total": 3104.75,
      "hotel": { "name": "Enchantment Resort", "price": 620.95, "currency": "USD", "stars": 4, "rating": 4.5, "review_count": 2080, "id": "..." }
    }
  ]
}
```

`offers` is sorted by check-in date; `stay_total` = `hotel.price × nights`.
The cheapest stay is `min_by(.hotel.price)` over `offers`.

### Recipes

Cheapest departure in a window:

```sh
gflights pricegraph --from JFK --to FCO --start 2026-07-06 --end 2026-07-10 --duration 15 --json \
  | jq '.offers | min_by(.price)'
```

Cheapest nonstop offer:

```sh
gflights offers --from JFK --to FCO --depart 2026-07-06 --return 2026-07-21 --json \
  | jq '.offers | map(select(.stops == 0)) | min_by(.price)'
```

Just the booking URL:

```sh
gflights offers --from JFK --to FCO --depart 2026-07-06 --return 2026-07-21 --json | jq -r '.url'
```

## Streaming (`--stream`)

`offers`, `pricegraph`, and `deals` accept `--stream`, which emits
newline-delimited JSON (NDJSON) — one object per line, flushed as it happens —
instead of one aggregate document. `--json` is unchanged; `--stream` is a
separate mode. Every line carries a `type`, and every stream ends with exactly
one `done` line, so an empty result and a died-halfway result are
distinguishable (no more retry-on-empty guessing).

`deals` is the one that streams *incrementally over time*: each destination's
deal is emitted the moment it completes, so the first result arrives in ~6s
instead of after the whole ~minutes-long sweep. `pricegraph` and `offers` fetch
in one shot, so their lines arrive together — but you still get NDJSON rows (no
big-array parse) and, for `offers`, `price_range` **first**, before the
itineraries.

```
$ gflights deals --from ORF --preset us-major --start … --end … --stream
{"type":"meta","command":"deals","from":"ORF","destinations":30,"range_start":"…","currency":"USD"}
{"type":"deal","dest":"ATL","price":80,"typical":119,"discount":32.8,"depart":"…","return":"…","currency":"USD"}
{"type":"deal","dest":"MCO","price":123,"typical":162,"discount":24.3,"depart":"…",...}
{"type":"failure","dest":"SEA","reason":"no offers with a price"}
{"type":"done","count":28,"failures":2}
```

Line types: `meta` (query echo, first), `deal` (deals), `fare` (pricegraph, one
per departure date), `price_range` (offers, emitted before any `offer`),
`offer` (offers), `failure` (a destination that yielded nothing, after retry),
`done` (always last, with `count` emitted and `failures`). In `deals --stream`,
`--min-discount` still filters inline; `--sort` and `--limit` are ignored (the
consumer orders the stream).

## Exit codes

| Code | Meaning                                                                  |
|------|--------------------------------------------------------------------------|
| `0`  | Success.                                                                 |
| `1`  | Runtime error (network, upstream rejection, no results, etc.).           |
| `2`  | Invalid CLI invocation (missing required flag, bad value).               |

## Testing

```sh
go test ./cmd/gflights -short   # offline: flag parsing, validation, output formatting
go test ./cmd/gflights          # + live end-to-end canaries (needs Chrome/Chromium, ~1 min)
```

The `TestLive*` tests run every subcommand in-process against the real Google
backends through the headless-browser session and assert the JSON output still
carries real prices, flights, and hotels. When Google changes a request format,
response schema, or gates an endpoint, these fail first — run them (or the
`flights` package's live tests) to pinpoint what broke.
