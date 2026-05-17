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
gflights pricegraph   cheapest round-trip price per departure date over a date range
gflights offers       detailed flight offers for a specific departure (+ return) date
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

## Exit codes

| Code | Meaning                                                                  |
|------|--------------------------------------------------------------------------|
| `0`  | Success.                                                                 |
| `1`  | Runtime error (network, upstream rejection, no results, etc.).           |
| `2`  | Invalid CLI invocation (missing required flag, bad value).               |
