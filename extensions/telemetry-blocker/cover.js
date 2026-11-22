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
  document.dispatchEvent(new Event("telemetry-cover-replaced"));
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
                font-family: -apple-system, system-ui, sans-serif; }
        main { margin-left: 18vw; max-width: 60vw; }
        .mode { color: #aaa9a4; font-weight: 700; letter-spacing: .12em; text-transform: uppercase; font-size: 15px; }
        .reason { font-size: 30px; margin: 18px 0; }
        .closed { color: #aaa9a4; font-size: 16px; margin-bottom: 18px; }
        button { font-size: 15px; padding: 6px 16px; }
      </style>
      <div class="page"><main>
        <div class="mode"></div><div class="reason"></div><div class="closed"></div>
        <button class="back">Go back</button> <button class="instead" hidden>Watch it as a normal video</button>
      </main></div>`;
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
    return host;
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
  }

  function hide() {
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
  }

  const check = () => chrome.runtime.sendMessage({ type: "check", host: location.hostname, url: location.href }, apply);
  check();
  chrome.runtime.onMessage.addListener((message) => {
    if (message.type === "decision") apply(message.decision);
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
    cover?.remove();
    cover = guard = mediaTimer = null;
  });
})();
