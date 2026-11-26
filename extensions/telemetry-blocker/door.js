// The wording of the door screen and the counter, from the collector's
// budget status (GET /api/telemetry/budget). Runs before cover.js in every
// page and touches no page; cover.js puts the words on screen. Every line
// states a fact and never scolds (docs/executive-function-principles.md).
globalThis.telemetryDoor = (() => {
  // "45 min", "1 h 40 min", "2 h".
  function minutes(value) {
    const total = Math.max(0, Math.round(value || 0));
    if (total < 60) return `${total} min`;
    const hours = Math.floor(total / 60);
    const rest = total % 60;
    return rest ? `${hours} h ${rest} min` : `${hours} h`;
  }
  const times = (value) => `${Math.round((value || 0) * 100) / 100}×`;
  const KINDS = { day: "daytime", distraction: "distraction time", bedtime: "bedtime" };
  const clock = (iso) => new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  // Whole minutes from now until iso, never less than zero.
  const minutesUntil = (iso, now) => Math.max(0, Math.ceil((Date.parse(iso) - now) / 60000));

  const budgetOf = (status) => (status && status.budget && status.budget.enabled ? status.budget : null);

  // "3× now. 10 min = 30 min of budget. Left today: 1 h 40 min." With
  // grayscale kept off the parts are named.
  function costLine(budget) {
    const rate = budget.rate || {};
    const total = rate.total || 0;
    let now = `${times(total)} now.`;
    if (rate.grayscale_off > 0) {
      now = `${times(total)} now: ${times(rate.base)} ${KINDS[rate.kind] || rate.kind || ""} + ${times(rate.grayscale_off)} with grayscale off.`;
    }
    const settings = budget.settings || {};
    const use = settings.window_use_minutes || 10;
    const borrow = settings.borrow_multiplier || 1;
    if (!(budget.left > 0)) {
      let line = `${now} Borrowing: ${use} min = ${minutes(use * total * borrow)} of tomorrow's budget.`;
      if (budget.borrowable != null) line += ` Tomorrow can still lend ${minutes(budget.borrowable)}.`;
      return line;
    }
    let line = `${now} ${use} min = ${minutes(use * total)} of budget. Left today: ${minutes(budget.left)}.`;
    if (budget.left < use * total) line += ` After that, each minute costs tomorrow ${times(total * borrow)}.`;
    return line;
  }

  // Today's habits: the done ones, and the open ones with what each earns.
  function habits(budget) {
    return (budget.habits || []).map((habit) => ({
      id: habit.id,
      done: !!habit.done,
      text: habit.done ? `${habit.name}: done, +${minutes(habit.value)}` : `${habit.name}: +${minutes(habit.value)} when done`,
    }));
  }

  // "Bedtime in 25 min." in the wind-down; "It's bedtime (until 08:00)."
  // during it.
  function bedtimeLine(status, now) {
    const budget = budgetOf(status);
    if (!budget) return "";
    if (status.bedtime_until || (budget.rate && budget.rate.kind === "bedtime")) {
      return status.bedtime_until ? `It's bedtime (until ${clock(status.bedtime_until)}).` : "It's bedtime.";
    }
    if (budget.wind_down && budget.bedtime_at) return `Bedtime in ${minutes(minutesUntil(budget.bedtime_at, now))}.`;
    return "";
  }

  // What the door can do now: offline, waiting with its countdown,
  // unavailable with the reason, or available.
  function doorState(status, now) {
    if (!status || !status.connected) {
      return { kind: "offline", text: "The Telemetry server can't be reached, so there is no door right now." };
    }
    const budget = status.budget;
    const door = budget.door;
    if (door && door.state === "waiting") {
      const left = Math.max(0, Math.ceil((Date.parse(door.opens_at) - now) / 1000));
      const text = left > 0
        ? `Opens in ${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}. Cancelling costs nothing.`
        : "Opening now.";
      return { kind: "waiting", id: door.id, text };
    }
    if (!budget.door_available) {
      let text = budget.door_unavailable_reason || "No door is available right now.";
      if (budget.next_door_at) text += ` The next door can open at ${clock(budget.next_door_at)}.`;
      return { kind: "unavailable", text };
    }
    return { kind: "available", text: "" };
  }

  // The door screen's text, or null when the budget is off.
  function screen(status, now) {
    const budget = budgetOf(status);
    if (!budget) return null;
    return {
      cost: costLine(budget),
      habits: habits(budget),
      reasons: (budget.reasons || []).filter((reason) => typeof reason === "string" && reason.trim()),
      bedtime: bedtimeLine(status, now),
      door: doorState(status, now),
    };
  }

  // The counter's one line: the time used this sitting, or the window's use,
  // or the grace left; the rate; the budget left; and bedtime in the
  // wind-down. Empty when the budget is off.
  function counter(status, sittingSeconds, now) {
    const budget = budgetOf(status);
    if (!budget) return "";
    const parts = [];
    const door = budget.door;
    if (door && door.state === "grace") {
      parts.push(`${Math.max(1, minutesUntil(door.ends_at, now))} min to close or save`);
    } else if (door && door.state === "open" && door.use_minutes != null) {
      parts.push(`${Math.round(door.used_minutes || 0)} of ${Math.round(door.use_minutes)} min`);
    } else {
      parts.push(`${minutes(Math.floor((sittingSeconds || 0) / 60))} this sitting`);
    }
    parts.push(times(budget.rate && budget.rate.total));
    parts.push(budget.left > 0 ? `${minutes(budget.left)} left` : "borrowing from tomorrow");
    if (budget.wind_down && budget.bedtime_at) parts.push(`Bedtime in ${minutes(minutesUntil(budget.bedtime_at, now))}`);
    return parts.join(" · ");
  }

  return { minutes, screen, counter };
})();
