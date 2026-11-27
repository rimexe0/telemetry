// Activity's Places: where the phone was on a day, from /v1/places/day.
// Missing positions are missing: the line breaks where the phone recorded
// nothing for more than 15 minutes, and the list says so, except inside a
// stay, where the phone records nothing because it is standing still.

export type PlacePosition = { at: string; lat: number; lon: number; accuracy: number };
export type NearbyPlace = { name: string; kind: string; lat: number; lon: number; meters: number };
export type PlaceStay = { from: string; to: string; lat: number; lon: number; home: boolean; nearby: NearbyPlace[] | null };
export type HabitPlace = { habit_id: number; habit: string; name: string; lat: number; lon: number; radius_m: number };
export type PlacesDay = {
  date: string;
  timezone: string;
  from: string;
  to: string;
  positions: PlacePosition[] | null;
  stays: PlaceStay[] | null;
  rules: HabitPlace[] | null;
};

/** The poorest position drawn, as the server's stays and habit rules use. */
export const worstAccuracy = 100;
/** The longest time between two positions the line still joins. */
export const longestGap = 15 * 60_000;

export type Point = { at: number; lat: number; lon: number };
export type Span = { from: number; to: number };
export type Visit = Span & { place: HabitPlace };
export type ListItem = Span & { kind: 'home' | 'stay' | 'visit' | 'gap'; text: string };

/** Distance in meters between two points on the earth. */
export function meters(lat1: number, lon1: number, lat2: number, lon2: number): number {
  const radius = 6_371_000;
  const p1 = (lat1 * Math.PI) / 180, p2 = (lat2 * Math.PI) / 180;
  const dp = p2 - p1, dl = ((lon2 - lon1) * Math.PI) / 180;
  const a = Math.sin(dp / 2) ** 2 + Math.cos(p1) * Math.cos(p2) * Math.sin(dl / 2) ** 2;
  return 2 * radius * Math.asin(Math.sqrt(a));
}

/** The day's positions within 100 m, oldest first. */
export function usablePoints(positions: PlacePosition[] | null): Point[] {
  return (positions ?? [])
    .filter(position => position.accuracy <= worstAccuracy && Number.isFinite(Date.parse(position.at)))
    .map(position => ({ at: Date.parse(position.at), lat: position.lat, lon: position.lon }))
    .sort((a, b) => a.at - b.at);
}

/** The path as lines, broken wherever positions are more than 15 minutes apart. */
export function pathLines(points: Point[]): Point[][] {
  const lines: Point[][] = [];
  let current: Point[] = [];
  for (const point of points) {
    const last = current[current.length - 1];
    if (last && point.at - last.at > longestGap) {
      lines.push(current);
      current = [];
    }
    current.push(point);
  }
  if (current.length) lines.push(current);
  return lines;
}

/** What is left of a span once the given spans are taken out. */
function without(span: Span, taken: Span[]): Span[] {
  let left = [span];
  for (const cut of taken) {
    left = left.flatMap(part => {
      if (cut.to <= part.from || cut.from >= part.to) return [part];
      const pieces: Span[] = [];
      if (cut.from > part.from) pieces.push({ from: part.from, to: cut.from });
      if (cut.to < part.to) pieces.push({ from: cut.to, to: part.to });
      return pieces;
    });
  }
  return left;
}

/**
 * The stretches of the day longer than 15 minutes with no position, from the
 * day's start to its end (now, for today), leaving out the time inside stays.
 */
export function positionGaps(points: Point[], stays: Span[], from: number, to: number): Span[] {
  const edges = [from, ...points.map(point => point.at).filter(at => at > from && at < to), to];
  const gaps: Span[] = [];
  for (let i = 1; i < edges.length; i++) {
    if (edges[i] - edges[i - 1] <= longestGap) continue;
    for (const part of without({ from: edges[i - 1], to: edges[i] }, stays)) {
      if (part.to - part.from > longestGap) gaps.push(part);
    }
  }
  return gaps;
}

/**
 * The times the positions were inside a habit's place: runs of positions in
 * its radius no more than 15 minutes apart, from the first to the last.
 */
export function habitVisits(points: Point[], rules: HabitPlace[] | null): Visit[] {
  const visits: Visit[] = [];
  for (const place of rules ?? []) {
    let current: Visit | undefined;
    for (const point of points) {
      const inside = meters(point.lat, point.lon, place.lat, place.lon) <= place.radius_m;
      if (inside && current && point.at - current.to <= longestGap) {
        current.to = point.at;
        continue;
      }
      if (current) visits.push(current);
      current = inside ? { place, from: point.at, to: point.at } : undefined;
    }
    if (current) visits.push(current);
  }
  return visits.sort((a, b) => a.from - b.from);
}

/** A clock time in the owner's timezone, 24-hour. */
export function clock(at: number, timezone: string): string {
  return new Intl.DateTimeFormat('en-GB', { timeZone: timezone || 'UTC', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(at);
}

function localDate(at: number, timezone: string): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: timezone || 'UTC', year: 'numeric', month: '2-digit', day: '2-digit' }).format(at);
}

/** "under 1 min", "23 min", "1 h 5 min". */
export function duration(milliseconds: number): string {
  const minutes = Math.round(milliseconds / 60_000);
  if (minutes < 1) return 'under 1 min';
  if (minutes < 60) return `${minutes} min`;
  return minutes % 60 ? `${Math.floor(minutes / 60)} h ${minutes % 60} min` : `${minutes / 60} h`;
}

/** A stay's place: the habit place it was at, else the nearest named place, else nothing. */
export function stayName(stay: PlaceStay, visits: Visit[]): string {
  const span = { from: Date.parse(stay.from), to: Date.parse(stay.to) };
  const visit = visits.find(item => item.from <= span.to && item.to >= span.from);
  return visit?.place.name ?? stay.nearby?.[0]?.name ?? '';
}

/** A stay's label on the map and in the list: its time span and place. */
export function stayLabel(stay: PlaceStay, visits: Visit[], timezone: string): string {
  const span = `${clock(Date.parse(stay.from), timezone)}–${clock(Date.parse(stay.to), timezone)}`;
  if (stay.home) return `home ${span}`;
  const name = stayName(stay, visits);
  return name ? `${span} ${name}` : span;
}

/**
 * The day in time order: each stay, each time at a habit's place outside a
 * stay, and each stretch with no positions.
 *   home 08:23–09:10
 *   08:17 Karantina İskelesi · 1 min
 *   09:15–09:40 Kahve Dünyası · 25 min
 *   no positions 10:20–11:05
 */
export function dayList(day: PlacesDay): ListItem[] {
  const timezone = day.timezone;
  const from = Date.parse(day.from), to = Date.parse(day.to);
  const points = usablePoints(day.positions);
  const stays = day.stays ?? [];
  const staySpans = stays.map(stay => ({ from: Date.parse(stay.from), to: Date.parse(stay.to) }));
  const visits = habitVisits(points, day.rules);
  const items: ListItem[] = [];
  stays.forEach((stay, i) => {
    const span = staySpans[i];
    const text = stay.home ? stayLabel(stay, visits, timezone) : `${stayLabel(stay, visits, timezone)} · ${duration(span.to - span.from)}`;
    items.push({ ...span, kind: stay.home ? 'home' : 'stay', text });
  });
  for (const visit of visits) {
    if (staySpans.some(span => visit.from <= span.to && visit.to >= span.from)) continue;
    items.push({ from: visit.from, to: visit.to, kind: 'visit', text: `${clock(visit.from, timezone)} ${visit.place.name} · ${duration(visit.to - visit.from)}` });
  }
  // A day that ends at midnight ends at 24:00; today's ends now.
  const ended = localDate(to, timezone) !== day.date;
  for (const gap of positionGaps(points, staySpans, from, to)) {
    const text = gap.to < to ? `no positions ${clock(gap.from, timezone)}–${clock(gap.to, timezone)}`
      : ended ? `no positions ${clock(gap.from, timezone)}–24:00` : `no positions since ${clock(gap.from, timezone)}`;
    items.push({ ...gap, kind: 'gap', text });
  }
  return items.sort((a, b) => a.from - b.from || a.to - b.to);
}
