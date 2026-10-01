// Runs in every page before it loads. If Telemetry blocks the site right
// now, the page is stopped and covered with the reason; the address and
// history stay as they are. When the block ends, the cover goes away and the
// page reloads, so nothing is lost.
(() => {
  if (window.top !== window) return;
  let cover = null;
  let stopped = false;
  let mediaTimer = null;
  let guard = null;

  function pauseMedia() {
    for (const media of document.querySelectorAll("video, audio")) media.pause();
  }

  function build(decision) {
    const host = document.createElement("telemetry-cover");
    const shadow = host.attachShadow({ mode: "closed" });
    shadow.innerHTML = `
      <style>
        :host { all: initial; position: fixed; inset: 0; z-index: 2147483647; display: block; }
        .page { position: absolute; inset: 0; background: #111213; color: #eeeeeb; display: flex; align-items: center;
                font-family: -apple-system, system-ui, sans-serif; }
        main { margin-left: 18vw; max-width: 60vw; }
        .mode { color: #aaa9a4; font-weight: 700; letter-spacing: .12em; text-transform: uppercase; font-size: 15px; }
        .reason { font-size: 30px; margin: 18px 0; }
        .closed { color: #aaa9a4; font-size: 16px; margin-bottom: 18px; }
        button { font-size: 15px; padding: 6px 16px; }
      </style>
      <div class="page"><main>
        <div class="mode"></div><div class="reason"></div><div class="closed"></div>
        <button>Go back</button>
      </main></div>`;
    shadow.querySelector(".mode").textContent = decision.mode || "";
    shadow.querySelector(".reason").textContent = decision.reason || "This site is blocked right now.";
    shadow.querySelector(".closed").textContent = `${location.hostname} is closed right now. The page will come back when the block ends.`;
    shadow.querySelector("button").addEventListener("click", () => history.back());
    return host;
  }

  function show(decision) {
    if (!cover) cover = build(decision);
    if (!cover.isConnected) document.documentElement.append(cover);
    if (document.readyState !== "complete" && !stopped) {
      window.stop();
      stopped = true;
    }
    pauseMedia();
    mediaTimer ??= setInterval(pauseMedia, 500);
    // A page script that removes the cover gets it back at once.
    guard ??= new MutationObserver(() => cover && !cover.isConnected && document.documentElement.append(cover));
    guard.observe(document.documentElement, { childList: true });
  }

  function hide() {
    if (!cover) return;
    guard?.disconnect();
    clearInterval(mediaTimer);
    cover.remove();
    cover = guard = mediaTimer = null;
    // The page was stopped or paused while covered; load it again.
    location.reload();
  }

  function apply(decision) {
    if (decision && decision.blocked) show(decision);
    else hide();
  }

  chrome.runtime.sendMessage({ type: "check", host: location.hostname }, apply);
  chrome.runtime.onMessage.addListener((message) => {
    if (message.type === "decision") apply(message.decision);
  });
})();
