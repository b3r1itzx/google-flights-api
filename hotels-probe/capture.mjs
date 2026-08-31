// Capture Google Hotels live-pricing XHRs via the Chrome DevTools Protocol.
//
// WHY: Google Hotels' static HTML only carries date-INSENSITIVE catalog prices.
// The real, date-specific nightly rates are fetched by a post-load XHR (a
// `batchexecute` call) that updates the DOM via JS. This script launches headless
// Chrome, loads a dated hotel search, and records every `batchexecute` request
// AND its response body — revealing the rpcid + request schema + response shape we
// need to replay it from Go (the way the flights side calls GetShoppingResults).
//
// USAGE
//   node capture.mjs
//   node capture.mjs "https://www.google.com/travel/search?q=hotels in New York&checkin=2026-06-01&checkout=2026-06-11"
//
// REQUIREMENTS
//   - Node 18+ (uses global fetch + WebSocket; Node 22 confirmed).
//   - A Chrome/Chromium binary. Auto-detected on macOS/Linux/Windows, or set
//     CHROME_BIN=/path/to/chrome.
//
// OUTPUT
//   Writes ./capture-output.json (full requests + decoded f.req + response bodies)
//   and prints a summary. SEND ME capture-output.json.
//
// NOTES
//   - A consent-bypass cookie is set so this works outside the US/EU too. If you
//     still land on a consent page, run once non-headless to accept, reusing the
//     same --user-data-dir (see CHROME_PROFILE below).
//   - If no batchexecute calls are captured, increase CAPTURE_MS or check that the
//     URL actually shows priced hotel cards in a normal browser.

import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { existsSync, writeFileSync } from "node:fs";
import { platform, tmpdir } from "node:os";
import { join } from "node:path";

const NAV_URL =
  process.argv[2] ||
  "https://www.google.com/travel/search?q=hotels in New York&checkin=2026-06-01&checkout=2026-06-11";
const PORT = Number(process.env.CHROME_PORT || 9222);
const CAPTURE_MS = Number(process.env.CAPTURE_MS || 18000);
const CHROME_PROFILE = process.env.CHROME_PROFILE || join(tmpdir(), "gh-chrome-profile");
const OUT_FILE = join(process.cwd(), "capture-output.json");

function findChrome() {
  if (process.env.CHROME_BIN) {
    if (existsSync(process.env.CHROME_BIN)) return process.env.CHROME_BIN;
    console.error(`CHROME_BIN set to "${process.env.CHROME_BIN}" but that path does not exist.`);
    process.exit(1);
  }
  const byOS = {
    darwin: [
      "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
      "/Applications/Chromium.app/Contents/MacOS/Chromium",
      "/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
    ],
    linux: [
      "/usr/bin/google-chrome",
      "/usr/bin/google-chrome-stable",
      "/usr/bin/chromium",
      "/usr/bin/chromium-browser",
      "/snap/bin/chromium",
    ],
    win32: [
      "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
      "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
    ],
  };
  return (byOS[platform()] || byOS.linux).find((p) => existsSync(p));
}

const chromeBin = findChrome();
if (!chromeBin) {
  console.error(
    "No Chrome/Chromium binary found. Set CHROME_BIN=/path/to/chrome and retry."
  );
  process.exit(1);
}

const headless = process.env.HEADFUL ? [] : ["--headless=new"];
const proc = spawn(chromeBin, [
  ...headless,
  "--no-sandbox",
  "--disable-gpu",
  "--disable-dev-shm-usage",
  "--no-first-run",
  "--no-default-browser-check",
  `--remote-debugging-port=${PORT}`,
  `--user-data-dir=${CHROME_PROFILE}`,
  "about:blank",
]);
let chromeStderr = "";
proc.stderr.on("data", (d) => (chromeStderr += d.toString()));
proc.on("error", (err) => {
  console.error(`Failed to launch Chrome at "${chromeBin}": ${err.message}`);
  process.exit(1);
});
proc.on("exit", (code, sig) => {
  if (code && code !== 0) {
    console.error(`Chrome exited early (code=${code} sig=${sig}).`);
    if (chromeStderr) console.error(chromeStderr.slice(0, 800));
  }
});

async function getWsUrl() {
  for (let i = 0; i < 75; i++) {
    try {
      const r = await fetch(`http://127.0.0.1:${PORT}/json/version`);
      const j = await r.json();
      if (j.webSocketDebuggerUrl) return j.webSocketDebuggerUrl;
    } catch {}
    await sleep(200);
  }
  throw new Error("CDP endpoint never came up — Chrome may have failed to launch.");
}

// --- minimal promise-based CDP client over the websocket ---
const wsUrl = await getWsUrl();
const ws = new WebSocket(wsUrl);
await new Promise((res, rej) => {
  ws.addEventListener("open", res, { once: true });
  ws.addEventListener("error", rej, { once: true });
});

let nextId = 0;
const inflight = new Map(); // id -> {resolve,reject}
const listeners = []; // event handlers

function cmd(method, params = {}) {
  const id = ++nextId;
  ws.send(JSON.stringify({ id, method, params }));
  return new Promise((resolve, reject) => inflight.set(id, { resolve, reject }));
}

ws.addEventListener("message", (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && inflight.has(msg.id)) {
    const { resolve, reject } = inflight.get(msg.id);
    inflight.delete(msg.id);
    msg.error ? reject(new Error(msg.error.message)) : resolve(msg.result);
    return;
  }
  for (const fn of listeners) fn(msg);
});

// --- capture state ---
const requests = new Map(); // requestId -> {rpcids, path, url, fReq}
const finished = []; // requestIds that finished, in order

listeners.push((msg) => {
  if (msg.method === "Network.requestWillBeSent") {
    const { requestId, request } = msg.params;
    if (!request.url.includes("batchexecute")) return;
    let rpcids = null,
      path = null;
    try {
      const u = new URL(request.url);
      rpcids = u.searchParams.get("rpcids");
      path = u.pathname;
    } catch {}
    let fReq = null;
    const m = /(?:^|&)f\.req=([^&]*)/.exec(request.postData || "");
    if (m) {
      try {
        fReq = decodeURIComponent(m[1]);
      } catch {
        fReq = m[1];
      }
    }
    requests.set(requestId, { rpcids, path, url: request.url, fReq });
  }
  if (msg.method === "Network.loadingFinished") {
    if (requests.has(msg.params.requestId)) finished.push(msg.params.requestId);
  }
});

await cmd("Network.enable");
await cmd("Page.enable");
await cmd("Runtime.enable");

// Bypass Google's consent interstitial across regions.
const now = Math.floor(Date.now() / 1000);
for (const domain of [".google.com", ".www.google.com"]) {
  await cmd("Network.setCookie", {
    name: "SOCS",
    value: "CAESEwgDEgk0ODE3Nzk3MjQaAmVuIAEaBgiA_LyaBg",
    domain,
    path: "/",
    expires: now + 60 * 60 * 24 * 365,
  }).catch(() => {});
  await cmd("Network.setCookie", {
    name: "CONSENT",
    value: "YES+cb.20210720-07-p0.en+FX+410",
    domain,
    path: "/",
    expires: now + 60 * 60 * 24 * 365,
  }).catch(() => {});
}

console.error(`Loading: ${NAV_URL}`);
await cmd("Page.navigate", { url: NAV_URL });

// Let the page settle, then scroll to trigger lazy pricing requests.
await sleep(4000);
for (let y = 1000; y <= 6000; y += 1000) {
  await cmd("Runtime.evaluate", { expression: `window.scrollTo(0, ${y})` }).catch(() => {});
  await sleep(900);
}

await sleep(Math.max(0, CAPTURE_MS - 4000 - 6 * 900));

// Pull response bodies for finished batchexecute requests.
const records = [];
for (const requestId of finished) {
  const meta = requests.get(requestId);
  let body = null,
    base64 = false;
  try {
    const r = await cmd("Network.getResponseBody", { requestId });
    body = r.body;
    base64 = r.base64Encoded;
  } catch (e) {
    body = `(could not fetch body: ${e.message})`;
  }
  records.push({ ...meta, base64Encoded: base64, responseBody: body });
}
// Also include requests we saw but that never reported finished (best effort).
for (const [requestId, meta] of requests) {
  if (!finished.includes(requestId)) {
    records.push({ ...meta, base64Encoded: false, responseBody: "(no loadingFinished seen)" });
  }
}

writeFileSync(OUT_FILE, JSON.stringify({ navUrl: NAV_URL, capturedAt: new Date().toISOString(), records }, null, 2));

console.error(`\n=== captured ${records.length} batchexecute request(s) ===`);
const byRpcid = {};
for (const r of records) {
  byRpcid[r.rpcids] = (byRpcid[r.rpcids] || 0) + 1;
}
for (const [rpcid, n] of Object.entries(byRpcid)) {
  console.error(`  rpcids=${rpcid}  x${n}`);
}
for (const r of records.slice(0, 12)) {
  console.error("\nrpcids:", r.rpcids, "  path:", r.path);
  if (r.fReq) console.error("f.req :", r.fReq.slice(0, 400));
  if (typeof r.responseBody === "string")
    console.error("resp  :", r.responseBody.slice(0, 200).replace(/\n/g, " "));
}
console.error(`\nFull detail written to: ${OUT_FILE}`);
console.error("Send me capture-output.json.");

ws.close();
proc.kill("SIGTERM");
setTimeout(() => proc.kill("SIGKILL"), 2000);
process.exit(0);
