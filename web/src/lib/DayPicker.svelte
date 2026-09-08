<script lang="ts">
  import { dayKey, dayStart } from './health';

  export let value = '';
  export let onchange: (date: string) => void = () => undefined;
  export let disabled = false;

  const DAY_MS = 24 * 60 * 60 * 1000;
  const MONTH_FORMAT = new Intl.DateTimeFormat('en-GB', {
    timeZone: 'Europe/Istanbul',
    month: 'long',
    year: 'numeric'
  });
  const DATE_FORMAT = new Intl.DateTimeFormat('en-GB', {
    timeZone: 'Europe/Istanbul',
    day: '2-digit',
    month: 'short',
    year: 'numeric'
  });

  type CalendarCell = { date: string; day: number; outside: boolean } | null;

  let open = false;
  let viewYear = 0;
  let viewMonth = 0;
  let lastValue = '';

  $: selectedDate = validDate(value) ? value : todayKey();
  $: if (selectedDate !== lastValue) {
    lastValue = selectedDate;
    const parsed = dateParts(selectedDate);
    viewYear = parsed.year;
    viewMonth = parsed.month;
  }
  $: monthDate = new Date(Date.UTC(viewYear, viewMonth, 1, 12));
  $: monthLabel = Number.isFinite(monthDate.getTime()) ? MONTH_FORMAT.format(monthDate) : '';
  $: cells = monthCells(viewYear, viewMonth);
  $: selectedLabel = DATE_FORMAT.format(new Date(dayStart(selectedDate) + 12 * 60 * 60 * 1000));

  function validDate(date: string): boolean {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !Number.isFinite(dayStart(date))) return false;
    const parts = dateParts(date);
    return dateFor(parts.year, parts.month, parts.day) === date;
  }

  function dateParts(date: string): { year: number; month: number; day: number } {
    const [year, month, day] = date.split('-').map(Number);
    return { year, month: month - 1, day };
  }

  function dateFor(year: number, month: number, day: number): string {
    const date = new Date(Date.UTC(year, month, day, 12));
    return date.toISOString().slice(0, 10);
  }

  function todayKey(): string {
    return dayKey(Date.now());
  }

  function shiftDay(date: string, amount: number): string {
    return dayKey(dayStart(date) + amount * DAY_MS);
  }

  function changeDay(date: string): void {
    if (disabled || !validDate(date)) return;
    onchange(date);
    open = false;
  }

  function toggle(): void {
    if (disabled) return;
    open = !open;
    if (open) {
      const parsed = dateParts(selectedDate);
      viewYear = parsed.year;
      viewMonth = parsed.month;
    }
  }

  function moveMonth(amount: number): void {
    if (disabled) return;
    const next = new Date(Date.UTC(viewYear, viewMonth + amount, 1, 12));
    viewYear = next.getUTCFullYear();
    viewMonth = next.getUTCMonth();
  }

  function monthCells(year: number, month: number): CalendarCell[] {
    if (!Number.isFinite(year) || !Number.isFinite(month)) return [];
    const first = new Date(Date.UTC(year, month, 1, 12));
    const leading = (first.getUTCDay() + 6) % 7;
    const count = new Date(Date.UTC(year, month + 1, 0, 12)).getUTCDate();
    const cells: CalendarCell[] = Array.from({ length: leading }, () => null);
    for (let day = 1; day <= count; day += 1) {
      cells.push({ date: dateFor(year, month, day), day, outside: false });
    }
    while (cells.length % 7 !== 0) cells.push(null);
    return cells;
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) {
      event.preventDefault();
      open = false;
    }
  }
</script>

<svelte:window on:keydown={handleKeydown} />

<div class="day-picker">
  <div class="day-picker-row">
    <button type="button" class="day-step" aria-label="Previous day" disabled={disabled} on:click={() => changeDay(shiftDay(selectedDate, -1))}>←</button>
    <button
      type="button"
      class="date-button"
      aria-label={`Choose date, ${selectedLabel}`}
      aria-haspopup="dialog"
      aria-expanded={open}
      disabled={disabled}
      on:click={toggle}
    >{selectedLabel}</button>
    <button type="button" class="day-step" aria-label="Next day" disabled={disabled} on:click={() => changeDay(shiftDay(selectedDate, 1))}>→</button>
    <button type="button" class="today-button" aria-label="Go to today" disabled={disabled || selectedDate === todayKey()} on:click={() => changeDay(todayKey())}>Today</button>
  </div>

  {#if open}
    <div class="calendar-popover" role="dialog" aria-label="Choose date">
      <div class="calendar-head">
        <button type="button" class="month-step" aria-label="Previous month" on:click={() => moveMonth(-1)}>←</button>
        <span aria-live="polite">{monthLabel}</span>
        <button type="button" class="month-step" aria-label="Next month" on:click={() => moveMonth(1)}>→</button>
      </div>
      <div class="weekdays" aria-hidden="true">
        {#each ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'] as weekday}
          <span>{weekday}</span>
        {/each}
      </div>
      <div class="calendar-grid">
        {#each cells as cell}
          {#if cell}
            <button
              type="button"
              class:selected={cell.date === selectedDate}
              class:today={cell.date === todayKey()}
              aria-label={cell.date}
              aria-current={cell.date === selectedDate ? 'date' : undefined}
              on:click={() => changeDay(cell.date)}
            >{cell.day}</button>
          {:else}
            <span class="calendar-empty" aria-hidden="true"></span>
          {/if}
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .day-picker { position: relative; min-width: 0; }
  .day-picker-row { display: flex; align-items: center; gap: 4px; }
  button { border: 1px solid var(--line); border-radius: 0; background: var(--panel); color: var(--fg); cursor: pointer; font: inherit; }
  button:hover, button:focus-visible { outline: 1px solid var(--fg); outline-offset: 1px; }
  button:disabled { cursor: wait; opacity: .5; }
  .day-step { width: 27px; height: 27px; padding: 0; line-height: 1; }
  .date-button { min-width: 136px; height: 27px; padding: 0 7px; text-align: left; white-space: nowrap; }
  .today-button { height: 27px; padding: 0 7px; }
  .calendar-popover { position: absolute; z-index: 10; top: calc(100% + 6px); left: 0; width: 244px; border: 1px solid var(--line); background: var(--panel); color: var(--fg); box-shadow: 0 8px 24px rgba(0, 0, 0, .25); padding: 8px; }
  .calendar-head { display: grid; grid-template-columns: 27px 1fr 27px; align-items: center; gap: 5px; margin-bottom: 7px; text-align: center; font-size: .75rem; font-weight: 700; }
  .month-step { width: 27px; height: 25px; padding: 0; line-height: 1; }
  .weekdays, .calendar-grid { display: grid; grid-template-columns: repeat(7, 1fr); gap: 2px; }
  .weekdays { margin-bottom: 2px; color: var(--muted); font-size: .6rem; text-align: center; }
  .calendar-grid button, .calendar-empty { min-width: 0; height: 27px; padding: 0; text-align: center; font-size: .7rem; }
  .calendar-grid button { background: var(--panel); }
  .calendar-grid button.selected { background: var(--fg); color: var(--bg); }
  .calendar-grid button.today { box-shadow: inset 0 0 0 1px var(--muted); }
  .calendar-grid button.selected.today { box-shadow: inset 0 0 0 1px var(--bg); }
</style>
