// Runs cover.js against a fake page: node cover.test.mjs
import fs from "node:fs";
import vm from "node:vm";
const src = fs.readFileSync(new URL("./cover.js", import.meta.url), "utf8");
const doorSrc = fs.readFileSync(new URL("./door.js", import.meta.url), "utf8");
// door loads door.js first, as the manifest does; budget is what the
// collector answers for the budget, and callResult for a door call.
function world({ readyState, visibility, blocked, trim, instead, pathname = "/a", found = {}, door = false, budget, distraction, callResult = { status: 200, body: {} } }) {
  const w = { parts: {}, assigned: [], reloads: 0, stops: 0, paused: 0, clicks: 0, listeners: {}, messages: [], visibility, covered: false, styled: false, timers: [], found,
    calls: [], backs: 0, chipShown: false, budget, callResult };
  const el = (tag) => tag === "style"
    ? { get isConnected() { return w.styled; }, remove() { w.styled = false; }, set textContent(v) { w.css = v; } }
    : { tag, children: [], text: "", set textContent(v) { this.text = v; }, append(...nodes) { this.children.push(...nodes); }, addEventListener(n, f) { this.click = f; },
        style: { setProperty: (k, v, p) => ((tag === "telemetry-counter" ? (w.chipStyle ||= {}) : (w.coverStyle ||= {}))[k] = v + " " + p) },
        attachShadow: () => ({ innerHTML: "", querySelector: (q) => (w.parts[q] ||= { hidden: q === ".instead", children: [], replaceChildren(...nodes) { this.children = nodes; }, addEventListener(n, f) { this.click = f; }, set textContent(v) { this.text = v; if (q === ".closed") w.closedText = v; } }) }),
        get isConnected() { return tag === "telemetry-counter" ? w.chipShown : w.covered; }, remove() { if (tag === "telemetry-counter") w.chipShown = false; else w.covered = false; } };
  const visible = { checkVisibility: () => true };
  const document = { readyState, get visibilityState() { return w.visibility; },
    createElement: el, querySelectorAll: (q) => (q === "video, audio" ? [{ pause: () => w.paused++ }] : w.found[q] ? [visible] : []),
    querySelector: (q) => (q.includes("autonav") && w.found.autoplay ? { closest: () => null, click: () => w.clicks++ } : w.found[q] ? visible : null),
    documentElement: { append(node) { if (node.tag === "telemetry-counter") w.chipShown = true; else if (node.attachShadow) w.covered = true; else w.styled = true; } },
    addEventListener: (n, f) => (w.listeners[n] = f), dispatchEvent() {} };
  const window = { stop: () => w.stops++ }; window.top = window;
  const location = { hostname: "x.com", href: "https://x.com" + pathname, pathname, reload: () => w.reloads++, assign: (a) => w.assigned.push(a) };
  w.location = location;
  const answer = (url) => ({ blocked: typeof blocked === "function" ? blocked(url) : blocked, trim: typeof trim === "function" ? trim(url) : trim, instead, distraction });
  const reply = (m) => {
    if (m.type === "check") return answer(m.url);
    if (m.type === "budget") return w.budget;
    if (m.type === "sitting") return w.budget && { status: w.budget, sitting: 600 };
    if (m.type === "call") { w.calls.push(m.path); return w.callResult; }
  };
  const ctx = { window, document, location, history: { back: () => w.backs++ }, Event: class {},
    setInterval: (f) => w.timers.push(f), clearInterval: (id) => { if (id) w.timers[id - 1] = () => {}; }, MutationObserver: class { observe() {} disconnect() {} },
    chrome: { runtime: { sendMessage: (m, cb) => { w.messages.push(m); cb(reply(m)); }, onMessage: { addListener: (f) => {
      w.send = (b, t, more = {}) => f({ type: "decision", decision: { blocked: b, trim: t, ...more } });
      w.budgetChanged = () => f({ type: "budget-changed" });
    } } } } };
  vm.createContext(ctx);
  if (door) vm.runInContext(doorSrc, ctx);
  vm.runInContext(src, ctx);
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

// The door on a covered page (budget-and-habits.md).
const soon = (minutes) => new Date(Date.now() + minutes * 60000).toISOString();
const budget = (more = {}) => ({ connected: true, now: new Date().toISOString(), budget: { enabled: true, day: "2026-11-26", budget: 150, spent: 42.5, left: 107.5,
  earned: 30, penalty: 0, debt: 0, debt_tomorrow: 0, borrowable: 90, rate: { total: 3, base: 3, grayscale_off: 0, kind: "day" },
  in_distraction_time: false, bedtime_at: soon(25), wind_down: false, grayscale_declined: false, door: null, door_available: true,
  door_unavailable_reason: "", next_door_at: null, habits: [{ id: 1, name: "Walk", done: true, value: 30 }, { id: 2, name: "Read", done: false, value: 30 }],
  reasons: ["I want my evenings for drawing."], settings: { wait_minutes: 10, window_minutes: 60, window_use_minutes: 10, grace_minutes: 1, borrow_multiplier: 2 }, ...more } });
const trusted = { isTrusted: true };
w = world({ readyState: "complete", visibility: "visible", blocked: true, door: true, budget: budget() });
check("a covered page shows the door", w.covered && w.parts[".door"].hidden === false && w.parts[".back"].hidden === true);
check("with the rate and what the window costs", w.parts[".cost"].text === "3× now. 10 min = 30 min of budget. Left today: 1 h 48 min.");
check("today's habits with what each earns", w.parts[".habits"].children.length === 2 && w.parts[".habits"].children[1].children[0].text === "Read: +30 min when done");
check("and the owner's reasons", w.parts[".reasons"].children[0].text === "I want my evenings for drawing.");
check("Still want it is offered", w.parts[".want"].hidden === false && w.parts[".cancel"].hidden === true);
w.parts[".want"].click({ isTrusted: false });
check("a page script cannot press it", w.calls.length === 0);
w.parts[".want"].click(trusted);
check("Still want it asks for a door", w.calls[0] === "door");
w.parts[".habits"].children[1].children[1].click(trusted);
check("a habit is ticked from the door", w.calls[1] === "habits/2/tick");
w.parts[".door-back"].click(trusted);
check("Back is recorded and goes back", w.calls[2] === "door/back" && w.backs === 1);
w.callResult = { status: 409, body: { error: "cooling_down", message: "The window is cooling down." } };
w.parts[".want"].click(trusted);
check("a refusal shows its message", w.parts[".state"].text === "The window is cooling down.");
w.budgetChanged(); w.tick();
check("which goes once the budget changes", w.parts[".state"].text === "");
w.budget = budget({ door: { id: 7, state: "waiting", requested_at: soon(0), opens_at: soon(9.5), window_end: soon(70), ends_at: soon(20), used_minutes: 0, use_minutes: 10, borrowing: false } });
w.callResult = { status: 200, body: {} };
w.budgetChanged();
check("a waiting door counts down", w.parts[".state"].text.startsWith("Opens in 9:") && w.parts[".want"].hidden === true && w.parts[".cancel"].hidden === false);
w.parts[".cancel"].click(trusted);
check("and can be cancelled", w.calls.at(-1) === "door/7/cancel");
w.budget = budget({ door_available: false, door_unavailable_reason: "Tomorrow is down to its floor, so nothing more can be borrowed." });
w.budgetChanged();
check("an unavailable door says why", w.parts[".state"].text.startsWith("Tomorrow is down") && w.parts[".want"].hidden === true);
w.budget = { ...budget(), connected: false };
w.budgetChanged();
check("without the server there is no door", w.parts[".state"].text.includes("can't be reached") && w.parts[".want"].hidden === true);
w = world({ readyState: "complete", visibility: "visible", blocked: true, door: true, budget: { connected: true, budget: { enabled: false } } });
check("with the budget off the cover is as before", w.covered && w.parts[".door"].hidden === true && w.parts[".back"].hidden === false);

// The counter on an open distraction.
w = world({ readyState: "complete", visibility: "visible", blocked: false, distraction: true, door: true, budget: budget() });
check("an open distraction shows the counter", w.chipShown && !w.covered && w.parts[".chip"].text === "10 min this sitting · 3× · 1 h 48 min left");
check("which lets clicks through", w.chipStyle["pointer-events"] === "none important");
w.send(true);
check("and goes when the site closes", !w.chipShown && w.covered);
w = world({ readyState: "complete", visibility: "visible", blocked: false, distraction: false, door: true, budget: budget() });
check("other sites have no counter", !w.chipShown);
w = world({ readyState: "complete", visibility: "visible", blocked: false, distraction: true, door: true, budget: { connected: true, budget: { enabled: false } } });
check("with the budget off there is no counter", !w.chipShown);
