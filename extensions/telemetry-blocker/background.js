// Asks the local Telemetry collector whether a site is blocked right now and
// tells each page's cover script. The collector follows the approved plan,
// offline too. If it cannot be reached, nothing is blocked.
const COLLECTOR = "http://127.0.0.1:5600/api/telemetry/site?host=";
const STATUS = "http://127.0.0.1:5600/api/telemetry/status";

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

// Every 30 seconds, the shortest alarm Chrome allows.
chrome.alarms.create("recheck", { periodInMinutes: 0.5 });
chrome.alarms.onAlarm.addListener(() => {
  badge();
  refreshTabs();
});
chrome.runtime.onStartup.addListener(badge);
badge();
refreshTabs();
