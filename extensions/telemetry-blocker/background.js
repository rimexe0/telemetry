// Asks the local Telemetry collector whether a site is blocked right now and
// tells each page's cover script. The collector follows the approved plan,
// offline too. If it cannot be reached, nothing is blocked.
const COLLECTOR = "http://127.0.0.1:5600/api/telemetry/site?host=";
const STATUS = "http://127.0.0.1:5600/api/telemetry/status";
const EVENTS = "ws://127.0.0.1:5600/api/telemetry/events";

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

// The cover script asks when a page starts loading.
chrome.runtime.onMessage.addListener((message, sender, reply) => {
  if (message.type !== "check") return false;
  decision(message.host, message.url).then(reply);
  return true;
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
      if (result && result.blocked) chrome.scripting.executeScript({ target: { tabId: tab.id }, files: ["cover.js"] }).catch(() => {});
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
    keepAlive = setInterval(() => socket.send("ping"), 20000);
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
async function updateIfNeeded(wanted) {
  if (!wanted || wanted === chrome.runtime.getManifest().version) return;
  const { reloadedFor } = await chrome.storage.local.get("reloadedFor");
  if (reloadedFor === wanted) return;
  await chrome.storage.local.set({ reloadedFor: wanted });
  chrome.runtime.reload();
}

// Pages open during an update keep the old cover script, which can no longer
// hear this one; a new one takes over from it.
chrome.runtime.onInstalled.addListener(async (details) => {
  if (details.reason !== "update") return;
  for (const tab of await chrome.tabs.query({ url: ["http://*/*", "https://*/*"], discarded: false })) {
    chrome.scripting.executeScript({ target: { tabId: tab.id }, files: ["cover.js"] }).catch(() => {});
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
