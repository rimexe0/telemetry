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
    tabs: { query: async () => [], sendMessage: async () => {}, onActivated: event(), onUpdated: event() },
    windows: { onFocusChanged: event() },
    alarms: { create() {}, onAlarm: event() },
    action: { setBadgeText() {}, setBadgeBackgroundColor() {} },
    scripting: { executeScript: async () => {} },
  };
  class WebSocket {
    static OPEN = 1;
    constructor(address) { this.address = address; this.readyState = 0; w.sockets.push(this); }
    send() {}
  }
  const ctx = { chrome, WebSocket, URL, console, setTimeout: () => 0, setInterval: () => 0, clearInterval() {},
    fetch: async () => ({ ok: true, json: async () => ({ connected: true, blocking_sites: [] }) }) };
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
