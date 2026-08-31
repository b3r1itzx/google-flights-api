# Capturing Google Hotels live-pricing XHRs

`capture.mjs` records the post-load `batchexecute` request(s) Google Hotels uses
to fetch **date-specific** nightly rates, plus their response bodies. We need this
because the static search HTML only has date-insensitive catalog prices (see
`FINDINGS.md`). The capture reveals the rpcid + request schema + response shape so
we can replay it from Go like the flights side does.

## Requirements

- **Node 18+** (Node 22 confirmed). Check: `node --version`.
- **Chrome or Chromium** installed. Auto-detected on macOS / Linux / Windows.
  Override with `CHROME_BIN=/path/to/chrome` if needed.

## Run it

From this folder:

```sh
node capture.mjs
```

That uses the default URL (New York, 2026-06-01 → 06-11). To capture your own
scenario, pass a dated search URL:

```sh
node capture.mjs "https://www.google.com/travel/search?q=hotels in New York&checkin=2026-06-01&checkout=2026-06-11"
```

It launches headless Chrome, loads the page, scrolls to trigger lazy pricing,
waits ~18s, then writes **`capture-output.json`** and prints a summary.

## Send me the result

Send back **`capture-output.json`** (or paste the summary the script prints). That
file contains, for each `batchexecute` call:

- `rpcids` — the RPC id(s) (the hotel-pricing one is what we're hunting)
- `f.req` — the decoded request payload (the schema: how dates/filters are encoded)
- `responseBody` — the raw response (how prices come back)

## Troubleshooting

- **"CDP endpoint never came up" / Chrome exits early.** Make sure Chrome runs
  normally first. On Linux without a sandbox, the `--no-sandbox` flag (already set)
  is required.
- **You land on a Google consent page** (results never load). The script sets a
  consent-bypass cookie, but if your region still blocks it, run once with a visible
  browser to click "Accept", reusing the same profile, then re-run headless:

  ```sh
  HEADFUL=1 CHROME_PROFILE=./gh-profile node capture.mjs   # accept consent, close window
  CHROME_PROFILE=./gh-profile node capture.mjs              # headless, reuses cookies
  ```

- **0 batchexecute calls captured.** Increase the capture window:

  ```sh
  CAPTURE_MS=30000 node capture.mjs
  ```

  and confirm the URL actually shows priced hotel cards in a normal browser.

- **Custom Chrome path:**

  ```sh
  CHROME_BIN="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" node capture.mjs
  ```

## What I'll do with it

From `f.req` I'll derive the request schema (place/feature-id + check-in/out + star
& price filters), and from `responseBody` the price-parsing path — then implement a
Go `hotels` package that replays the call through the existing `Session`, mirroring
the flights `GetOffers` / `GetPriceGraph` API and the `gflights` CLI.
