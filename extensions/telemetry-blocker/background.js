// Asks the local Telemetry collector whether a site is blocked right now and
// replaces blocked tabs with the reason. The collector follows the approved
// plan, offline too. If it cannot be reached, nothing is blocked.
const COLLECTOR = "http://127.0.0.1:5600/api/telemetry/site?host=";
const STATUS = "http://127.0.0.1:5600/api/telemetry/status";
const BLOCKED_PAGE = chrome.runtime.getURL("blocked.html");

async function decision(host) {
  try {
    const response = await fetch(COLLECTOR + encodeURIComponent(host), { cache: "no-store" });
    return response.ok ? await response.json() : null;
  } catch {
    return null;
  }
}

async function check(tabId, url) {
  if (!url || !/^https?:/.test(url)) return;
  const host = new URL(url).hostname;
  const result = await decision(host);
  if (!result || !result.blocked) return;
  const params = new URLSearchParams({ host, mode: result.mode || "", reason: result.reason || "" });
  chrome.tabs.update(tabId, { url: `${BLOCKED_PAGE}?${params}` });
}

chrome.tabs.onUpdated.addListener((tabId, change, tab) => {
  if (change.url || change.status === "loading") check(tabId, change.url || tab.url);
});
chrome.tabs.onActivated.addListener(async ({ tabId }) => {
  const tab = await chrome.tabs.get(tabId);
  check(tabId, tab.url);
});

// The badge shows whether sites are being closed right now.
async function badge() {
  try {
    const response = await fetch(STATUS, { cache: "no-store" });
    const status = await response.json();
    const blocking = status.connected && (status.block_all || status.blocking_sites.length > 0);
    chrome.action.setBadgeText({ text: blocking ? "ON" : "" });
    chrome.action.setBadgeBackgroundColor({ color: "#8a2b2b" });
  } catch {
    chrome.action.setBadgeText({ text: "!" });
    chrome.action.setBadgeBackgroundColor({ color: "#777" });
  }
}

// A work block can start while a site is already open.
chrome.alarms.create("recheck", { periodInMinutes: 1 });
chrome.alarms.onAlarm.addListener(async () => {
  badge();
  for (const tab of await chrome.tabs.query({})) check(tab.id, tab.url);
});
badge();
