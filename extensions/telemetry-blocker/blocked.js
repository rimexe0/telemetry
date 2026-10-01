const params = new URLSearchParams(location.search);
document.getElementById("mode").textContent = params.get("mode") || "";
document.getElementById("reason").textContent = params.get("reason") || "This site is blocked right now.";
document.getElementById("closed").textContent = `${params.get("host") || "This site"} is closed right now.`;
document.getElementById("leave").addEventListener("click", async () => {
  const tab = await chrome.tabs.getCurrent();
  chrome.tabs.update(tab.id, { url: "chrome://newtab/" });
});
