// Runs cover.js against a fake page: node cover.test.mjs
import fs from "node:fs";
import vm from "node:vm";
const src = fs.readFileSync(new URL("./cover.js", import.meta.url), "utf8");
function world({ readyState, visibility, blocked }) {
  const w = { reloads: 0, stops: 0, listeners: {}, messages: [], visibility, covered: false };
  const el = () => ({ attachShadow: () => ({ innerHTML: "", querySelector: () => ({ addEventListener() {}, textContent: "" }) }),
    get isConnected() { return w.covered; }, remove() { w.covered = false; } });
  const document = { readyState, get visibilityState() { return w.visibility; },
    createElement: el, querySelectorAll: () => [], documentElement: { append() { w.covered = true; } },
    addEventListener: (n, f) => (w.listeners[n] = f) };
  const window = { stop: () => w.stops++ }; window.top = window;
  const ctx = { window, document, location: { hostname: "x.com", reload: () => w.reloads++ }, history: {},
    setInterval: () => 1, clearInterval() {}, MutationObserver: class { observe() {} disconnect() {} },
    chrome: { runtime: { sendMessage: (m, cb) => cb({ blocked }), onMessage: { addListener: (f) => (w.send = (b) => f({ type: "decision", decision: { blocked: b } })) } } } };
  vm.runInNewContext(src, ctx);
  w.seen = (v) => { w.visibility = v; w.listeners.visibilitychange(); };
  return w;
}
const check = (name, ok) => { console.log(ok ? "ok  " : "FAIL", name); if (!ok) process.exitCode = 1; };
let w = world({ readyState: "complete", visibility: "visible", blocked: false });
w.send(true); check("loaded page is covered, not stopped", w.covered && w.stops === 0);
w.send(false); check("loaded page is uncovered without reload", !w.covered && w.reloads === 0);
w = world({ readyState: "loading", visibility: "visible", blocked: true });
check("blocked page is stopped and covered", w.covered && w.stops === 1);
w.send(false); check("stopped page reloads when visible", !w.covered && w.reloads === 1);
w = world({ readyState: "loading", visibility: "hidden", blocked: true });
w.send(false); check("stopped background page does not reload", !w.covered && w.reloads === 0);
w.seen("visible"); check("it reloads once the tab is looked at", w.reloads === 1);
w = world({ readyState: "loading", visibility: "hidden", blocked: true });
w.send(false); w.send(true); w.seen("visible");
check("a new block cancels the waiting reload", w.covered && w.reloads === 0);
w.send(false); check("and it reloads when that block ends", w.reloads === 1);
w = world({ readyState: "complete", visibility: "hidden", blocked: false });
w.send(true); w.send(false); w.seen("visible"); check("loaded background page never reloads", w.reloads === 0);
