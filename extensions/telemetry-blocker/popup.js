const mode = document.getElementById("mode");
const reason = document.getElementById("reason");
const detail = document.getElementById("detail");

function time(value) {
  return new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

fetch("http://127.0.0.1:5600/api/telemetry/status", { cache: "no-store" })
  .then((response) => response.json())
  .then((status) => {
    if (!status.connected) {
      mode.textContent = "Telemetry";
      reason.textContent = "No current plan decisions, so nothing is blocked.";
      detail.textContent = status.decisions_fetched_at ? `Last update ${time(status.decisions_fetched_at)}.` : "The collector has not received decisions yet.";
      return;
    }
    mode.textContent = status.mode || "Telemetry";
    reason.textContent = status.reason || "";
    if (status.block_all) {
      detail.textContent = "All sites except essentials and the Telemetry panel are closed right now.";
    } else if (status.blocking_sites.length > 0) {
      detail.innerHTML = "Closed right now:<ul></ul>";
      for (const site of status.blocking_sites) {
        const item = document.createElement("li");
        item.textContent = site;
        detail.querySelector("ul").append(item);
      }
    } else {
      detail.textContent = status.next_block_at
        ? `Nothing is blocked now. Next: ${status.next_block_mode} at ${time(status.next_block_at)}.`
        : "Nothing is blocked now, and no blocking period is planned in the next 12 hours.";
    }
  })
  .catch(() => {
    mode.textContent = "Telemetry";
    reason.textContent = "The Telemetry collector is not running, so nothing is blocked.";
  });
