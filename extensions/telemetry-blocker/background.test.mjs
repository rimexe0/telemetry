// Runs background.js against fake Chromium and Firefox APIs: node background.test.mjs
import fs from "node:fs";
import vm from "node:vm";
const src = fs.readFileSync(new URL("./background.js", import.meta.url), "utf8");
const check = (name, ok) => { console.log(ok ? "ok  " : "FAIL", name); if (!ok) process.exitCode = 1; };
const event = () => ({ addListener() {} });
const settle = () => new Promise((resolve) => setTimeout(resolve, 10));

// firefox leaves out what Firefox does not have: runtime.requestUpdateCheck.
function browser({ firefox, installType }) {
  const w = { checks: 0, reloads: 0, sockets: [], errors: [] };
  const runtime = {
    getManifest: () => ({ version: "0.11.0" }), reload: () => w.reloads++,
    onMessage: event(), onUpdateAvailable: event(), onInstalled: event(), onStartup: event(),
  };
  if (!firefox) runtime.requestUpdateCheck = async () => { w.checks++; return { status: "update_available" }; };
  const chrome = {
    runtime,
    extension: { isAllowedIncognitoAccess: async () => true },
    management: { getSelf: async () => ({ installType }) },
    storage: { local: { get: async () => ({}), set: async () => {} }, session: { get: async () => ({}), set: async () => {} } },
    tabs: { query: async (q) => (q && q.windowId ? [{ id: 5, url: "https://x.com/home", title: "Home / X", active: true, incognito: false }] : []), sendMessage: async () => {}, onActivated: event(), onUpdated: event() },
    windows: { onFocusChanged: event(), getLastFocused: async () => ({ id: 1, focused: true }) },
    alarms: { create() {}, onAlarm: event() },
    action: { setBadgeText() {}, setBadgeBackgroundColor() {} },
    scripting: { executeScript: async () => {} },
  };
  class WebSocket {
    static OPEN = 1;
    constructor(address) { this.address = address; this.readyState = 0; w.sockets.push(this); }
    send() {}
  }
  w.requests = [];
  const ctx = { chrome, WebSocket, URL, console, setTimeout: () => 0, setInterval: () => 0, clearInterval() {},
    fetch: async (address, options) => {
      w.requests.push({ address, body: options && options.body });
      return { ok: true, status: 200, json: async () => (address.endsWith("/api/0/info") ? { hostname: "plagiarism.local" } : { connected: true, blocking_sites: [] }) };
    } };
  if (firefox) ctx.browser = { runtime: { getBrowserInfo: async () => ({ name: "LibreWolf" }) } };
  w.ctx = ctx;
  vm.createContext(ctx);
  process.on("unhandledRejection", (error) => w.errors.push(error));
  vm.runInContext(src, ctx);
  w.hello = (version) => w.sockets[0].onmessage({ data: JSON.stringify({ type: "hello", extension_version: version }) });
  return w;
}

let w = browser({ firefox: false, installType: "admin" });
w.hello("0.12.0");
await settle();
check("Chromium installed by policy is asked to fetch the update", w.checks === 1 && w.reloads === 0 && w.errors.length === 0);

w = browser({ firefox: true, installType: "admin" });
check("the background script starts in Firefox", w.sockets.length === 1 && w.sockets[0].address === "ws://127.0.0.1:5600/api/telemetry/events");
w.hello("0.12.0");
await settle();
check("Firefox installed by policy waits for its own update check", w.checks === 0 && w.reloads === 0 && w.errors.length === 0);

w = browser({ firefox: true, installType: "development" });
w.hello("0.12.0");
await settle();
check("a copy loaded from the folder reloads to take the new version", w.reloads === 1);
w.hello("0.11.0");
await settle();
check("and not for the version it has", w.reloads === 1);

// LibreWolf records the tab in front itself, in the web watcher's form, so
// no ActivityWatch add-on is needed there; Helium keeps its watcher.
w = browser({ firefox: true, installType: "admin" });
await w.ctx.recordTab();
const beat = w.requests.find((r) => r.address.includes("/heartbeat"));
check("LibreWolf records the tab in front", beat && beat.address.startsWith("http://127.0.0.1:5600/api/0/buckets/aw-watcher-web-librewolf_plagiarism.local/heartbeat?pulsetime=")
  && JSON.parse(beat.body).data.url === "https://x.com/home" && w.requests.some((r) => r.address.endsWith("/buckets/aw-watcher-web-librewolf_plagiarism.local")));
w = browser({ firefox: false, installType: "admin" });
await w.ctx.recordTab();
check("Chromium leaves it to the web watcher", !w.requests.some((r) => r.address.includes("/api/0/")));
