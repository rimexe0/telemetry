// Asks the local Telemetry collector whether a site is blocked right now and
// tells each page's cover script. The collector follows the approved plan,
// offline too. If it cannot be reached, nothing is blocked.
const COLLECTOR = "http://127.0.0.1:5600/api/telemetry/site?host=";
const STATUS = "http://127.0.0.1:5600/api/telemetry/status";

async function decision(host) {
  try {
    const response = await fetch(COLLECTOR + encodeURIComponent(host), { cache: "no-store" });
    return response.ok ? await response.json() : null;
  } catch {
    return null;
  }
}

// The cover script asks when a page starts loading.
chrome.runtime.onMessage.addListener((message, sender, reply) => {
  if (message.type !== "check") return false;
  decision(message.host).then(reply);
  return true;
});

// Open tabs follow a block starting or ending without being reloaded.
async function refreshTabs() {
  for (const tab of await chrome.tabs.query({ url: ["http://*/*", "https://*/*"] })) {
    const result = await decision(new URL(tab.url).hostname);
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

chrome.alarms.create("recheck", { periodInMinutes: 1 });
chrome.alarms.onAlarm.addListener(() => {
  badge();
  refreshTabs();
});
badge();
refreshTabs();
