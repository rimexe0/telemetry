// Asks the local Telemetry collector whether a site is blocked right now and
// tells each page's cover script. The collector follows the approved plan,
// offline too. If it cannot be reached, nothing is blocked.
const COLLECTOR = "http://127.0.0.1:5600/api/telemetry/site?host=";
const STATUS = "http://127.0.0.1:5600/api/telemetry/status";
const EVENTS = "ws://127.0.0.1:5600/api/telemetry/events";
// The distraction budget, and the door's calls, which the collector makes to
// the server with this computer's credentials; the extension holds none.
const BUDGET = "http://127.0.0.1:5600/api/telemetry/budget";
const DEVICE = "http://127.0.0.1:5600/api/telemetry/device/";
// The scripts every page runs, in order.
const PAGE_SCRIPTS = ["door.js", "cover.js"];

// The status request is also this browser checking in: the collector hides
// a browser whose extension stays quiet while sites are blocked. It says
// whether the extension may run in private windows, since without that
// those windows are not covered.
async function statusAddress() {
  let incognito = false;
  try {
    incognito = await chrome.extension.isAllowedIncognitoAccess();
  } catch {}
  return STATUS + "?incognito=" + (incognito ? "1" : "0");
}

// The page address goes along so a single page of a closed site can stay
// open, such as one music stream.
async function decision(host, address) {
  try {
    const response = await fetch(COLLECTOR + encodeURIComponent(host) + "&url=" + encodeURIComponent(address || ""), { cache: "no-store" });
    return response.ok ? await response.json() : null;
  } catch {
    return null;
  }
}

async function budget() {
  try {
    const response = await fetch(BUDGET, { cache: "no-store" });
    return response.ok ? await response.json() : null;
  } catch {
    return null;
  }
}

// One door call: the door, Back, cancelling a waiting door, a habit tick.
// The server's answer comes back as it is, refusals included.
async function call(path) {
  try {
    const response = await fetch(DEVICE + path, { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}", cache: "no-store" });
    let body = null;
    try {
      body = await response.json();
    } catch {}
    return { status: response.status, body };
  } catch {
    return { status: 0, body: { message: "The Telemetry collector is not running, so there is no door right now." } };
  }
}

// Other tabs showing the door or the counter follow a change made in one.
async function tellBudgetChanged() {
  for (const tab of await chrome.tabs.query({ url: ["http://*/*", "https://*/*"], discarded: false })) {
    chrome.tabs.sendMessage(tab.id, { type: "budget-changed" }).catch(() => {});
  }
}

// A sitting is time on distractions with no break longer than SITTING_GAP.
// Each visible counter beats every few seconds; the time between beats
// counts, at most BEAT_MAX, so two windows showing distractions do not count
// twice. It is kept in session storage, so it outlasts this service worker.
const SITTING_GAP = 5 * 60 * 1000;
const BEAT_MAX = 15 * 1000;
let sitting = null;
async function sittingSeconds() {
  if (!sitting) {
    try {
      sitting = (await chrome.storage.session.get("sitting")).sitting || null;
    } catch {}
  }
  const now = Date.now();
  if (!sitting || now - sitting.last > SITTING_GAP || now < sitting.last) {
    sitting = { used: 0, last: now };
  } else {
    sitting.used += Math.min(now - sitting.last, BEAT_MAX);
    sitting.last = now;
  }
  chrome.storage.session.set({ sitting }).catch(() => {});
  return Math.floor(sitting.used / 1000);
}

// The cover script asks when a page starts loading; the door screen and the
// counter ask for the budget and make the door's calls.
chrome.runtime.onMessage.addListener((message, sender, reply) => {
  switch (message.type) {
    case "check":
      decision(message.host, message.url).then(reply);
      return true;
    case "budget":
      budget().then(reply);
      return true;
    case "sitting":
      Promise.all([budget(), sittingSeconds()]).then(([status, seconds]) => reply({ status, sitting: seconds }));
      return true;
    case "call":
      if (typeof message.path !== "string") return false;
      call(message.path).then((result) => {
        if (result.status >= 200 && result.status < 300) tellBudgetChanged();
        reply(result);
      });
      return true;
  }
  return false;
});

// Open tabs follow a block starting or ending. Tabs the browser has put to
// sleep are left alone; their cover script asks again when they wake.
async function refreshTabs() {
  const decisions = new Map();
  for (const tab of await chrome.tabs.query({ url: ["http://*/*", "https://*/*"], discarded: false })) {
    if (tab.status === "unloaded") continue;
    if (!decisions.has(tab.url)) decisions.set(tab.url, await decision(new URL(tab.url).hostname, tab.url));
    const result = decisions.get(tab.url);
    try {
      await chrome.tabs.sendMessage(tab.id, { type: "decision", decision: result });
    } catch {
      // Tabs opened before the extension loaded have no cover script yet.
      if (result && result.blocked) chrome.scripting.executeScript({ target: { tabId: tab.id }, files: PAGE_SCRIPTS }).catch(() => {});
    }
  }
}

// The badge shows whether sites are being closed right now.
async function badge() {
  try {
    const response = await fetch(await statusAddress(), { cache: "no-store" });
    const status = await response.json();
    const blocking = status.connected && (status.block_all || status.blocking_sites.length > 0);
    chrome.action.setBadgeText({ text: blocking ? "ON" : "" });
    chrome.action.setBadgeBackgroundColor({ color: "#8a2b2b" });
  } catch {
    chrome.action.setBadgeText({ text: "!" });
    chrome.action.setBadgeBackgroundColor({ color: "#777" });
  }
}

// Each browser profile runs its own copy of this extension. Each copy tells
// the collector the tabs its windows show, so a window from a profile without
// the extension can be told apart by its title and hidden while sites are
// blocked.
async function reportTabs() {
  const socket = events;
  if (!socket || socket.readyState !== WebSocket.OPEN) return;
  const titles = (await chrome.tabs.query({ active: true })).map((tab) => tab.title || tab.url || "");
  socket.send(JSON.stringify({ type: "tabs", titles }));
}
chrome.tabs.onActivated.addListener(reportTabs);
chrome.tabs.onUpdated.addListener((tabId, change, tab) => {
  if (tab.active && (change.title || change.url)) reportTabs();
});
chrome.windows.onFocusChanged.addListener(reportTabs);

// The collector says at once when the decision in force changes, so open tabs
// follow within a second or two. Sending every 20 seconds keeps this service
// worker alive while the connection is open.
let events = null;
function listen() {
  if (events) return;
  const socket = new WebSocket(EVENTS);
  events = socket;
  let keepAlive = null;
  socket.onopen = () => {
    keepAlive = setInterval(reportTabs, 20000);
    reportTabs();
    badge();
    refreshTabs();
  };
  socket.onmessage = (event) => {
    const message = JSON.parse(event.data);
    if (message.type === "hello") updateIfNeeded(message.extension_version);
    if (message.type === "changed") {
      badge();
      refreshTabs();
    }
  };
  socket.onclose = () => {
    clearInterval(keepAlive);
    events = null;
    setTimeout(listen, 5000);
  };
}

// The updater keeps the newest extension in the folder this one loads from,
// and the collector names its version. Reloading takes it into use. It reloads
// once per version, so a copy loaded from another folder does not keep trying.
// A copy browser policy installed comes from the collector's packed one
// instead: the browser fetches it when asked to check.
async function updateIfNeeded(wanted) {
  if (!wanted || wanted === chrome.runtime.getManifest().version) return;
  const self = await chrome.management.getSelf().catch(() => null);
  if (self && self.installType === "admin") {
    chrome.runtime.requestUpdateCheck().catch(() => {});
    return;
  }
  const { reloadedFor } = await chrome.storage.local.get("reloadedFor");
  if (reloadedFor === wanted) return;
  await chrome.storage.local.set({ reloadedFor: wanted });
  chrome.runtime.reload();
}

chrome.runtime.onUpdateAvailable.addListener(() => chrome.runtime.reload());

// Pages open during an update keep the old cover script, which can no longer
// hear this one; a new one takes over from it.
chrome.runtime.onInstalled.addListener(async (details) => {
  if (details.reason !== "update") return;
  for (const tab of await chrome.tabs.query({ url: ["http://*/*", "https://*/*"], discarded: false })) {
    chrome.scripting.executeScript({ target: { tabId: tab.id }, files: PAGE_SCRIPTS }).catch(() => {});
  }
});

// The backup, and the check-in: every 30 seconds, the shortest alarm Chrome
// allows.
chrome.alarms.create("recheck", { periodInMinutes: 0.5 });
chrome.alarms.onAlarm.addListener(() => {
  listen();
  badge();
  refreshTabs();
});
chrome.runtime.onStartup.addListener(badge);
listen();
badge();
refreshTabs();
