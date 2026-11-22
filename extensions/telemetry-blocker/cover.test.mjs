// Runs cover.js against a fake page: node cover.test.mjs
import fs from "node:fs";
import vm from "node:vm";
const src = fs.readFileSync(new URL("./cover.js", import.meta.url), "utf8");
function world({ readyState, visibility, blocked, trim, instead, pathname = "/a", found = {} }) {
  const w = { parts: {}, assigned: [], reloads: 0, stops: 0, paused: 0, clicks: 0, listeners: {}, messages: [], visibility, covered: false, styled: false, timers: [], found };
  const el = (tag) => tag === "style"
    ? { get isConnected() { return w.styled; }, remove() { w.styled = false; }, set textContent(v) { w.css = v; } }
    : { style: { setProperty: (k, v, p) => ((w.coverStyle ||= {})[k] = v + " " + p) }, attachShadow: () => ({ innerHTML: "", querySelector: (q) => (w.parts[q] ||= { hidden: q === ".instead", addEventListener(n, f) { this.click = f; }, set textContent(v) { if (q === ".closed") w.closedText = v; } }) }),
        get isConnected() { return w.covered; }, remove() { w.covered = false; } };
  const visible = { checkVisibility: () => true };
  const document = { readyState, get visibilityState() { return w.visibility; },
    createElement: el, querySelectorAll: (q) => (q === "video, audio" ? [{ pause: () => w.paused++ }] : w.found[q] ? [visible] : []),
    querySelector: (q) => (q.includes("autonav") && w.found.autoplay ? { closest: () => null, click: () => w.clicks++ } : w.found[q] ? visible : null),
    documentElement: { append(node) { if (node.attachShadow) w.covered = true; else w.styled = true; } },
    addEventListener: (n, f) => (w.listeners[n] = f), dispatchEvent() {} };
  const window = { stop: () => w.stops++ }; window.top = window;
  const location = { hostname: "x.com", href: "https://x.com" + pathname, pathname, reload: () => w.reloads++, assign: (a) => w.assigned.push(a) };
  w.location = location;
  const answer = (url) => ({ blocked: typeof blocked === "function" ? blocked(url) : blocked, trim: typeof trim === "function" ? trim(url) : trim, instead });
  const ctx = { window, document, location, history: {}, Event: class {},
    setInterval: (f) => w.timers.push(f), clearInterval: (id) => { if (id) w.timers[id - 1] = () => {}; }, MutationObserver: class { observe() {} disconnect() {} },
    chrome: { runtime: { sendMessage: (m, cb) => { w.messages.push(m); cb(answer(m.url)); }, onMessage: { addListener: (f) => (w.send = (b, t) => f({ type: "decision", decision: { blocked: b, trim: t } })) } } } };
  vm.runInNewContext(src, ctx);
  w.tick = (n = 1) => { for (let i = 0; i < n; i++) for (const f of [...w.timers]) f(); };
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
w = world({ readyState: "complete", visibility: "visible", blocked: (url) => !url.includes("open") });
w.location.href = "https://x.com/open"; w.tick();
check("the page address is sent with each check", w.messages.at(-1).url === "https://x.com/open");
check("an open page of a closed site is uncovered when navigated to", !w.covered);
w.location.href = "https://x.com/other"; w.tick();
check("and covered again on the next page", w.covered);
w = world({ readyState: "complete", visibility: "visible", blocked: true });
w.listeners["telemetry-cover-replaced"]();
check("a new copy of the script takes over from the old one", !w.covered);

w = world({ readyState: "complete", visibility: "visible", blocked: true, trim: "youtube" });
check("a trimmed page is not covered but styled", !w.covered && w.styled && w.css.includes('ytd-browse[page-subtype="home"]'));
check("search results listing Shorts as ordinary results hide them", w.css.includes('ytd-search ytd-video-renderer:has(a[href^="/shorts/"])'));
w.tick(); check("and its media keeps playing", w.paused === 0);
w = world({ readyState: "complete", visibility: "visible", blocked: true, trim: "unknown" });
check("a trim this copy does not know covers the page", w.covered && !w.styled);
w = world({ readyState: "complete", visibility: "visible", blocked: true, trim: "youtube", pathname: "/watch", found: { autoplay: true, "ytd-watch-flexy #related": true } });
w.tick(); check("autoplay is turned off", w.clicks === 1);
w.tick(12); check("a page that looks as expected stays trimmed", !w.covered && w.styled);
w = world({ readyState: "complete", visibility: "visible", blocked: true, trim: "youtube", pathname: "/watch" });
w.tick(9); check("a page still loading its parts is given time", !w.covered);
w.tick(); check("a page missing the expected parts is covered", w.covered && !w.styled && w.closedText.includes("looks different"));
w = world({ readyState: "complete", visibility: "visible", blocked: true, trim: "youtube", found: { 'ytd-browse[page-subtype="home"]': true } });
w.tick(); w.found["ytd-compact-video-renderer"] = true; w.tick();
check("a feed showing through covers the page", w.covered && !w.styled);
w.send(true, "youtube"); check("and it stays covered on the next decision", w.covered && !w.styled);
w.send(false); w.send(true, "youtube"); check("until the block ends", !w.covered && w.styled);
w = world({ readyState: "complete", visibility: "visible", blocked: true, trim: (url) => (url.includes("watch") ? "youtube" : undefined), pathname: "/watch", found: { "ytd-watch-flexy #related": true } });
w.location.href = "https://x.com/shorts/a"; w.location.pathname = "/shorts/a"; w.tick();
check("a page of the site that is not trimmed is covered", w.covered && !w.styled);
w.location.href = "https://x.com/watch"; w.location.pathname = "/watch"; w.tick();
check("and trimmed again when going back to one that is", !w.covered && w.styled);
w.send(false); check("the style goes when the block ends", !w.covered && !w.styled);
w = world({ readyState: "complete", visibility: "visible", blocked: true });
check("the cover cannot be hidden by page styles", w.coverStyle.visibility === "visible important" && w.coverStyle["z-index"] === "2147483647 important");
w = world({ readyState: "loading", visibility: "visible", blocked: true, instead: "https://www.youtube.com/watch?v=abc", pathname: "/shorts/abc" });
check("a covered Short offers the same video on the trimmed page", w.covered && w.parts[".instead"].hidden === false && w.assigned.length === 0);
w.parts[".instead"].click(); check("and goes there only when clicked", w.assigned[0] === "https://www.youtube.com/watch?v=abc");
w = world({ readyState: "loading", visibility: "visible", blocked: true, pathname: "/shorts/abc" });
check("a covered page without one offers nothing", w.covered && w.parts[".instead"].hidden === true);
