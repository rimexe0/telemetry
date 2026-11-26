// Checks the door screen's and the counter's wording: node door.test.mjs
import fs from "node:fs";
import vm from "node:vm";
const ctx = {};
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(new URL("./door.js", import.meta.url), "utf8"), ctx);
const { screen, counter, minutes } = ctx.telemetryDoor;
const check = (name, ok, got) => { console.log(ok ? "ok  " : "FAIL", name, ok ? "" : JSON.stringify(got)); if (!ok) process.exitCode = 1; };

const now = Date.parse("2026-11-26T20:35:00Z");
const at = (m) => new Date(now + m * 60000).toISOString();
const status = (more = {}, outer = {}) => ({ connected: true, ...outer, budget: { enabled: true, budget: 150, spent: 42.5, left: 107.5, borrowable: 90,
  rate: { total: 3, base: 3, grayscale_off: 0, kind: "day" }, bedtime_at: at(25), wind_down: false, door: null, door_available: true,
  habits: [], reasons: [], settings: { window_use_minutes: 10, borrow_multiplier: 2 }, ...more } });

check("minutes read as hours and minutes", minutes(45) === "45 min" && minutes(100) === "1 h 40 min" && minutes(120) === "2 h" && minutes(-3) === "0 min");
check("the cost of a window", screen(status(), now).cost === "3× now. 10 min = 30 min of budget. Left today: 1 h 48 min.", screen(status(), now).cost);
let cost = screen(status({ left: 0 }), now).cost;
check("borrowing says what tomorrow pays", cost === "3× now. Borrowing: 10 min = 1 h of tomorrow's budget. Tomorrow can still lend 1 h 30 min.", cost);
cost = screen(status({ left: 12 }), now).cost;
check("a window the budget only partly pays", cost.endsWith("After that, each minute costs tomorrow 6×."), cost);
cost = screen(status({ rate: { total: 13, base: 10, grayscale_off: 3, kind: "bedtime" } }), now).cost;
check("the rate's parts with grayscale off", cost.startsWith("13× now: 10× bedtime + 3× with grayscale off."), cost);
check("the wind-down says when bedtime comes", screen(status({ wind_down: true }), now).bedtime === "Bedtime in 25 min.");
const bedtime = screen(status({ rate: { total: 10, base: 10, kind: "bedtime" } }, { bedtime_until: "2026-11-27T05:00:00Z" }), now).bedtime;
check("during bedtime it says until when", bedtime.startsWith("It's bedtime (until ") && bedtime.endsWith(")."), bedtime);
check("no bedtime line in the day", screen(status(), now).bedtime === "");
const next = screen(status({ door_available: false, door_unavailable_reason: "The window is cooling down.", next_door_at: at(40) }), now).door;
check("an unavailable door gives the reason and the next one", next.kind === "unavailable" && next.text.startsWith("The window is cooling down. The next door can open at "), next);
check("the budget off has no door screen", screen({ connected: true, budget: { enabled: false } }, now) === null && screen(null, now) === null);

check("the counter shows this sitting, the rate and what is left", counter(status(), 754, now) === "12 min this sitting · 3× · 1 h 48 min left", counter(status(), 754, now));
const open = { id: 7, state: "open", ends_at: at(5), used_minutes: 6.2, use_minutes: 10 };
check("in a window it shows its use", counter(status({ door: open }), 754, now) === "6 of 10 min · 3× · 1 h 48 min left", counter(status({ door: open }), 754, now));
const grace = { ...open, state: "grace", ends_at: at(0.6), used_minutes: 10 };
check("in grace the time to close or save", counter(status({ door: grace }), 754, now).startsWith("1 min to close or save · "), counter(status({ door: grace }), 754, now));
const borrowing = { ...open, use_minutes: null, borrowing: true };
check("a borrowing door in distraction time has no window", counter(status({ door: borrowing, left: 0 }), 120, now) === "2 min this sitting · 3× · borrowing from tomorrow");
check("the wind-down adds bedtime", counter(status({ wind_down: true }), 0, now).endsWith(" · Bedtime in 25 min"));
check("the budget off has no counter", counter({ connected: true, budget: { enabled: false } }, 100, now) === "");
