// Runs in every page before it loads. If Telemetry blocks the site right
// now, the page is stopped and covered with the reason; the address and
// history stay as they are. When the block ends the cover goes away. A page
// that was stopped before it loaded is loaded again, but only once its tab is
// looked at; a page that had already loaded is left as it was.
//
// Some pages of a closed site may stay usable with their feeds hidden: the
// decision then names a trim. If the page no longer looks the way the trim
// expects, or a feed shows through, the page is covered instead.
(() => {
  if (window.top !== window) return;
  // After an extension update, a new copy of this script takes over the page.
  // Firefox stops the old copy without telling it, so what it put on the
  // page is taken away here too.
  document.dispatchEvent(new Event("telemetry-cover-replaced"));
  for (const left of document.querySelectorAll("telemetry-cover, telemetry-counter, style[data-telemetry-trim]")) left.remove();
  let cover = null;
  let stopped = false;
  let mediaTimer = null;
  let guard = null;
  let reloadWhenSeen = false;
  let trimStyle = null;
  let trimTimer = null;
  let trimmed = null;
  // Set once a trim fails on this page; the site stays covered until the
  // block ends or the page is loaded again.
  let trimFailed = false;
  let ticksOnAddress = 0;
  // The door screen on the cover and the counter on an open distraction,
  // worded by door.js. Without it, or with the budget off, the cover is as
  // before and there is no counter.
  const words = globalThis.telemetryDoor;
  let coverShadow = null;
  let doorStatus = null;
  let doorNote = "";
  let doorId = null;
  let doorTimer = null;
  let doorTicks = 0;
  let chip = null;
  let chipText = null;
  let chipTimer = null;

  const hidden = (selectors) => selectors.join(",\n") + " { display: none !important; }";
  const TRIMS = {
    youtube: {
      css: hidden([
        // The home feed, the guide on the left and the notifications.
        'ytd-browse[page-subtype="home"]', "#guide", "tp-yt-app-drawer#guide", "ytd-mini-guide-renderer", "#guide-button",
        "ytd-notification-topbar-button-renderer",
        // Beside and under a video: suggestions and comments. A playlist
        // being watched stays.
        "ytd-watch-flexy #related", "ytd-watch-next-secondary-results-renderer", "ytd-watch-flexy #comments",
        // In the player: end screens, cards, next-video countdown and the
        // fullscreen grid of more videos.
        ".html5-endscreen", ".ytp-endscreen-content", ".ytp-ce-element", ".ytp-cards-teaser", ".ytp-cards-button",
        ".ytp-autonav-endscreen-countdown-overlay", ".ytp-fullscreen-grid", ".ytp-suggested-action",
        // In search results: Shorts and suggestion shelves.
        "ytd-search ytd-reel-shelf-renderer", "ytd-search grid-shelf-view-model", "ytd-search ytd-shelf-renderer",
        "ytd-search ytm-shorts-lockup-view-model", "ytd-search ytm-shorts-lockup-view-model-v2",
        // Since November 2026 search results also list Shorts as ordinary
        // results linking to /shorts/; left showing, they closed the site.
        'ytd-search ytd-video-renderer:has(a[href^="/shorts/"])', 'ytd-search yt-lockup-view-model:has(a[href^="/shorts/"])',
      ]),
      // Seen on screen, these mean the hiding stopped working.
      leaks: [
        'ytd-browse[page-subtype="home"] ytd-rich-item-renderer', "ytd-rich-grid-renderer ytd-rich-item-renderer",
        "ytd-watch-next-secondary-results-renderer yt-lockup-view-model", "ytd-compact-video-renderer",
        "ytd-comment-thread-renderer", "ytd-reel-item-renderer", "ytm-shorts-lockup-view-model", ".ytp-videowall-still",
        'ytd-search a[href^="/shorts/"]',
      ],
      // What a loaded page must have; without it the page has changed.
      expects: (path) => ({ "/": 'ytd-browse[page-subtype="home"]', "/results": "ytd-search", "/watch": "ytd-watch-flexy #related" })[path],
      // Autoplay would play suggestions one after another.
      tidy() {
        const autoplay = document.querySelector('.ytp-autonav-toggle-button[aria-checked="true"]');
        if (autoplay) (autoplay.closest("button") || autoplay).click();
      },
    },
    reddit: {
      css: hidden([
        "#left-sidebar-container", "#right-sidebar-container", "#mosaic-pdp-feed", "pdp-right-rail", "shreddit-sidebar-ad",
        "reddit-pdp-right-rail-post", "#navbar-menu-button",
      ]),
      leaks: ["reddit-pdp-right-rail-post", "#mosaic-pdp-feed shreddit-post", "#left-sidebar-container", "shreddit-feed"],
      expects: () => "shreddit-post",
      tidy() {},
    },
  };

  const onScreen = (element) => (element.checkVisibility ? element.checkVisibility() : element.getClientRects().length > 0);

  // Checked every second while a page is trimmed.
  function checkTrim() {
    const trim = TRIMS[trimmed?.trim];
    if (!trim) return;
    if (!trimStyle.isConnected) document.documentElement.append(trimStyle);
    trim.tidy();
    ticksOnAddress++;
    const leaked = trim.leaks.some((selector) => [...document.querySelectorAll(selector)].some(onScreen));
    const expected = trim.expects(location.pathname);
    const missing = expected && ticksOnAddress >= 10 && document.readyState === "complete" && !document.querySelector(expected);
    if (leaked || missing) {
      trimFailed = true;
      apply(trimmed);
    }
  }

  function trim(decision) {
    trimmed = decision;
    if (!trimStyle) {
      trimStyle = document.createElement("style");
      trimStyle.setAttribute("data-telemetry-trim", "");
      trimStyle.textContent = TRIMS[decision.trim].css;
    }
    if (!trimStyle.isConnected) document.documentElement.append(trimStyle);
    trimTimer ??= setInterval(checkTrim, 1000);
  }

  function untrim() {
    clearInterval(trimTimer);
    trimStyle?.remove();
    trimStyle = trimTimer = trimmed = null;
  }

  // A background tab stays stopped until the owner opens it.
  document.addEventListener("visibilitychange", () => {
    if (reloadWhenSeen && document.visibilityState === "visible") location.reload();
  });

  function pauseMedia() {
    for (const media of document.querySelectorAll("video, audio")) media.pause();
  }

  function build(decision) {
    const host = document.createElement("telemetry-cover");
    // Inline and important, so page styles cannot hide it. Reddit hides every
    // element it does not know, which a page changed without loading has.
    const forced = { all: "initial", position: "fixed", inset: "0", "z-index": "2147483647", display: "block", visibility: "visible", opacity: "1", "pointer-events": "auto" };
    for (const [name, value] of Object.entries(forced)) host.style.setProperty(name, value, "important");
    const shadow = host.attachShadow({ mode: "closed" });
    shadow.innerHTML = `
      <style>
        :host { all: initial; position: fixed; inset: 0; z-index: 2147483647; display: block; }
        .page { position: absolute; inset: 0; background: #111213; color: #eeeeeb; display: flex; align-items: center;
                font-family: -apple-system, system-ui, sans-serif; overflow: auto; }
        main { margin-left: 18vw; max-width: 60vw; padding: 32px 0; }
        .mode { color: #aaa9a4; font-weight: 700; letter-spacing: .12em; text-transform: uppercase; font-size: 15px; }
        .reason { font-size: 30px; margin: 18px 0; }
        .closed { color: #aaa9a4; font-size: 16px; margin-bottom: 18px; }
        button { font-size: 15px; padding: 6px 16px; }
        .door { font-size: 16px; line-height: 1.45; }
        .door [hidden] { display: none; }
        .cost { font-size: 18px; margin: 0 0 14px; }
        .bedtime { margin: 0 0 14px; }
        .heading { color: #aaa9a4; font-weight: 700; font-size: 13px; letter-spacing: .08em; text-transform: uppercase; margin-top: 14px; }
        ul { margin: 6px 0 0; padding-left: 20px; }
        li { margin: 4px 0; }
        li button { font-size: 13px; padding: 2px 10px; margin-left: 8px; }
        .state { color: #aaa9a4; margin: 16px 0 12px; min-height: 1.4em; }
      </style>
      <div class="page"><main>
        <div class="mode"></div><div class="reason"></div><div class="closed"></div>
        <button class="back">Go back</button> <button class="instead" hidden>Watch it as a normal video</button>
        <section class="door" hidden>
          <p class="cost"></p>
          <p class="bedtime"></p>
          <div class="heading habits-title">Today's habits</div><ul class="habits"></ul>
          <div class="heading reasons-title">Your reasons</div><ul class="reasons"></ul>
          <p class="state"></p>
          <button class="door-back">Back</button> <button class="want" hidden>Still want it</button> <button class="cancel" hidden>Cancel</button>
        </section>
      </main></div>`;
    coverShadow = shadow;
    shadow.querySelector(".mode").textContent = decision.mode || "";
    shadow.querySelector(".reason").textContent = decision.reason || "This site is blocked right now.";
    shadow.querySelector(".closed").textContent = decision.trimFailed
      ? `This ${location.hostname} page looks different from what the blocker knows, so the site is closed until the block ends.`
      : `${location.hostname} is closed right now. The page will come back when the block ends.`;
    shadow.querySelector(".back").addEventListener("click", () => history.back());
    // A Short is covered, since its page scrolls on into the Shorts feed; the
    // same video can play on the trimmed video page. Only the owner's click
    // goes there.
    const instead = shadow.querySelector(".instead");
    if (decision.instead && !decision.trimFailed) {
      instead.hidden = false;
      instead.addEventListener("click", () => location.assign(decision.instead));
    }
    // The door's buttons answer only the owner's own clicks, never a page
    // script's.
    shadow.querySelector(".door-back").addEventListener("click", (event) => {
      if (!event || !event.isTrusted) return;
      call("door/back");
      history.back();
    });
    shadow.querySelector(".want").addEventListener("click", (event) => event && event.isTrusted && call("door"));
    shadow.querySelector(".cancel").addEventListener("click", (event) => event && event.isTrusted && doorId != null && call(`door/${doorId}/cancel`));
    return host;
  }

  // Asks the collector for the budget and shows the door with it.
  function askBudget() {
    try {
      chrome.runtime.sendMessage({ type: "budget" }, (status) => {
        doorStatus = status && status.budget !== undefined ? status : null;
        renderDoor();
      });
    } catch {}
  }

  // Makes one of the door's calls through the collector, which holds this
  // computer's credentials. A refusal's message is shown as it came.
  function call(path) {
    doorNote = "";
    try {
      chrome.runtime.sendMessage({ type: "call", path }, (result) => {
        if (result && (result.status < 200 || result.status >= 300)) {
          doorNote = (result.body && result.body.message) || "That could not be done right now.";
        }
        askBudget();
      });
    } catch {}
  }

  function renderDoor() {
    if (!coverShadow || !words) return;
    const view = words.screen(doorStatus, Date.now());
    const part = (selector) => coverShadow.querySelector(selector);
    part(".door").hidden = !view;
    part(".back").hidden = !!view;
    if (!view) return;
    part(".cost").textContent = view.cost;
    part(".bedtime").textContent = view.bedtime;
    part(".bedtime").hidden = !view.bedtime;
    part(".habits-title").hidden = view.habits.length === 0;
    part(".habits").replaceChildren(...view.habits.map((habit) => {
      const row = document.createElement("li");
      const text = document.createElement("span");
      text.textContent = habit.text;
      const tick = document.createElement("button");
      tick.textContent = habit.done ? "Undo" : "Done";
      tick.addEventListener("click", (event) => event && event.isTrusted && call(`habits/${habit.id}/${habit.done ? "untick" : "tick"}`));
      row.append(text, tick);
      return row;
    }));
    part(".reasons-title").hidden = view.reasons.length === 0;
    part(".reasons").replaceChildren(...view.reasons.map((reason) => {
      const row = document.createElement("li");
      row.textContent = reason;
      return row;
    }));
    doorId = view.door.kind === "waiting" ? view.door.id : null;
    part(".state").textContent = doorNote || view.door.text;
    part(".want").hidden = view.door.kind !== "available";
    part(".cancel").hidden = view.door.kind !== "waiting";
  }

  // The wait counts down every second; the budget is asked again every 15.
  function followDoor() {
    askBudget();
    doorTimer ??= setInterval(() => {
      doorTicks++;
      if (doorTicks % 15 === 0) askBudget();
      else renderDoor();
    }, 1000);
  }

  function stopDoor() {
    clearInterval(doorTimer);
    doorTimer = null;
    doorStatus = null;
    doorNote = "";
  }

  // The counter: a small chip in a corner of an open distraction. It takes
  // no input and lets clicks through.
  function buildChip() {
    const host = document.createElement("telemetry-counter");
    const forced = { all: "initial", position: "fixed", left: "12px", bottom: "12px", "z-index": "2147483647", display: "block",
      visibility: "visible", opacity: "1", "pointer-events": "none" };
    for (const [name, value] of Object.entries(forced)) host.style.setProperty(name, value, "important");
    const shadow = host.attachShadow({ mode: "closed" });
    shadow.innerHTML = `
      <style>
        :host { all: initial; pointer-events: none; }
        .chip { font: 12px/1.3 -apple-system, system-ui, sans-serif; color: #eeeeeb; background: rgba(17, 18, 19, .82);
                padding: 4px 8px; border-radius: 6px; white-space: nowrap; }
      </style>
      <div class="chip"></div>`;
    chipText = shadow.querySelector(".chip");
    return host;
  }

  // Each beat counts this sitting in the background and shows the line.
  function beat() {
    if (document.visibilityState !== "visible") return;
    try {
      chrome.runtime.sendMessage({ type: "sitting" }, (reply) => {
        if (!chipTimer) return;
        const text = reply && reply.status !== undefined ? words.counter(reply.status, reply.sitting, Date.now()) : "";
        if (!text) {
          chip?.remove();
          return;
        }
        chip ??= buildChip();
        chipText.textContent = text;
        if (!chip.isConnected) document.documentElement.append(chip);
      });
    } catch {}
  }

  function counter(decision) {
    if (!words || !decision || decision.blocked || !decision.distraction) {
      clearInterval(chipTimer);
      chip?.remove();
      chipTimer = null;
      return;
    }
    if (chipTimer) return;
    chipTimer = setInterval(beat, 5000);
    beat();
  }

  function show(decision) {
    reloadWhenSeen = false;
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
    followDoor();
  }

  function hide() {
    stopDoor();
    if (!cover) return;
    guard?.disconnect();
    clearInterval(mediaTimer);
    cover.remove();
    cover = guard = mediaTimer = null;
    if (!stopped) return;
    if (document.visibilityState === "visible") location.reload();
    else reloadWhenSeen = true;
  }

  function apply(decision) {
    if (decision && decision.blocked && TRIMS[decision.trim] && !trimFailed) {
      hide();
      trim(decision);
    } else if (decision && decision.blocked) {
      untrim();
      show(trimFailed ? { ...decision, trimFailed } : decision);
    } else {
      untrim();
      trimFailed = false;
      hide();
    }
    counter(decision);
  }

  const check = () => chrome.runtime.sendMessage({ type: "check", host: location.hostname, url: location.href }, apply);
  check();
  chrome.runtime.onMessage.addListener((message) => {
    if (message.type === "decision") apply(message.decision);
    // A door or a tick changed the budget, here or in another tab.
    if (message.type === "budget-changed") {
      doorNote = "";
      if (cover) askBudget();
      if (chipTimer) beat();
    }
  });
  // Sites such as YouTube change page without loading one; ask again then,
  // because one page of a closed site may be open while the next is not.
  let address = location.href;
  const addressTimer = setInterval(() => {
    if (location.href === address) return;
    address = location.href;
    ticksOnAddress = 0;
    check();
  }, 1000);
  document.addEventListener("telemetry-cover-replaced", () => {
    guard?.disconnect();
    clearInterval(mediaTimer);
    clearInterval(addressTimer);
    untrim();
    stopDoor();
    counter(null);
    cover?.remove();
    cover = guard = mediaTimer = null;
  });
})();
