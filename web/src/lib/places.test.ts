import { describe, expect, it } from 'vitest';
import { dayList, duration, habitVisits, pathLines, positionGaps, usablePoints, type PlacePosition, type PlacesDay } from './places';

// Made-up places for tests, not the owner's.
const home = { lat: 41.018, lon: 29.0 };
const pier = { lat: 41.0, lon: 29.0 };
const cafe = { lat: 41.03, lon: 29.02 };
const at = (clock: string) => `2026-11-20T${clock}:00+03:00`;
const iso = (clock: string) => new Date(at(clock)).toISOString();
const position = (clock: string, place: { lat: number; lon: number }, accuracy = 10): PlacePosition => ({ at: iso(clock), ...place, accuracy });

function testDay(): PlacesDay {
  const positions: PlacePosition[] = [];
  for (let hour = 0; hour <= 8; hour++) positions.push(position(`${String(hour).padStart(2, '0')}:00`, home));
  positions.push(
    position('08:10', { lat: 41.009, lon: 29.0 }),
    position('08:12', { lat: 41.2, lon: 29.3 }, 400),
    position('08:17', pier),
    position('08:18', { lat: 41.0003, lon: 29.0 }),
    position('08:30', { lat: 41.01, lon: 29.01 }),
    position('09:15', cafe), position('09:30', cafe),
    position('09:50', home), position('10:20', home),
    position('11:05', { lat: 41.02, lon: 29.01 }), position('11:10', { lat: 41.021, lon: 29.01 })
  );
  return {
    date: '2026-11-20',
    timezone: 'Europe/Istanbul',
    from: iso('00:00'),
    to: '2026-11-20T21:00:00Z',
    positions,
    stays: [
      { from: iso('00:00'), to: iso('08:10'), ...home, home: true, nearby: [] },
      { from: iso('09:15'), to: iso('09:50'), ...cafe, home: false, nearby: [{ name: 'Test café', kind: 'amenity=cafe', ...cafe, meters: 4 }] },
      { from: iso('09:50'), to: iso('10:20'), ...home, home: true, nearby: [] }
    ],
    rules: [{ habit_id: 1, habit: 'Morning walk', name: 'Test pier', ...pier, radius_m: 100 }]
  };
}

describe('the path', () => {
  it('leaves out positions worse than 100 m', () => {
    const points = usablePoints(testDay().positions);
    expect(points.some(point => point.lat === 41.2)).toBe(false);
    expect(points).toHaveLength(testDay().positions!.length - 1);
  });

  it('breaks the line where positions are more than 15 minutes apart', () => {
    const lines = pathLines(usablePoints([position('08:00', home), position('08:15', home), position('08:31', pier), position('08:40', pier)]));
    expect(lines.map(line => line.length)).toEqual([2, 2]);
    expect(pathLines([])).toEqual([]);
  });
});

describe('gaps', () => {
  it('are the stretches over 15 minutes without positions, outside stays', () => {
    const day = testDay();
    const points = usablePoints(day.positions);
    const stays = day.stays!.map(stay => ({ from: Date.parse(stay.from), to: Date.parse(stay.to) }));
    const gaps = positionGaps(points, stays, Date.parse(day.from), Date.parse(day.to));
    expect(gaps.map(gap => [new Date(gap.from).toISOString(), new Date(gap.to).toISOString()])).toEqual([
      [iso('08:30'), iso('09:15')],
      [iso('10:20'), iso('11:05')],
      [iso('11:10'), '2026-11-20T21:00:00.000Z']
    ]);
  });

  it('cover a whole day without positions', () => {
    expect(positionGaps([], [], Date.parse(iso('00:00')), Date.parse(iso('12:00')))).toEqual([{ from: Date.parse(iso('00:00')), to: Date.parse(iso('12:00')) }]);
  });
});

describe('habit places', () => {
  it('are reached by positions within their radius, joined across short gaps', () => {
    const day = testDay();
    const visits = habitVisits(usablePoints(day.positions), day.rules);
    expect(visits).toHaveLength(1);
    expect([visits[0].from, visits[0].to]).toEqual([Date.parse(iso('08:17')), Date.parse(iso('08:18'))]);
  });

  it('do not count an imprecise position', () => {
    const rules = testDay().rules;
    expect(habitVisits(usablePoints([position('08:17', pier, 150)]), rules)).toEqual([]);
  });
});

describe('the list', () => {
  it('has the stays, the habit places and the missing positions in time order', () => {
    expect(dayList(testDay()).map(item => item.text)).toEqual([
      'home 00:00–08:10',
      '08:17 Test pier · 1 min',
      'no positions 08:30–09:15',
      '09:15–09:50 Test café · 35 min',
      'home 09:50–10:20',
      'no positions 10:20–11:05',
      'no positions 11:10–24:00'
    ]);
  });

  it('says since when for today', () => {
    const day = { ...testDay(), to: iso('12:00') };
    expect(dayList(day).at(-1)?.text).toBe('no positions since 11:10');
  });

  it('names a stay at a habit place after the habit place', () => {
    const day = testDay();
    day.positions!.push(position('09:20', cafe));
    day.rules = [{ habit_id: 2, habit: 'Coffee', name: 'Test corner', ...cafe, radius_m: 50 }];
    const texts = dayList(day).map(item => item.text);
    expect(texts).toContain('09:15–09:50 Test corner · 35 min');
    expect(texts.some(text => text.startsWith('09:15 Test corner'))).toBe(false);
  });

  it('shows a stay with no named place by its times', () => {
    const day = testDay();
    day.stays![1].nearby = null;
    expect(dayList(day)).toContainEqual(expect.objectContaining({ kind: 'stay', text: '09:15–09:50 · 35 min' }));
  });

  it('is empty-handed only by saying so', () => {
    const day: PlacesDay = { ...testDay(), positions: null, stays: null, rules: null };
    expect(dayList(day).map(item => item.text)).toEqual(['no positions 00:00–24:00']);
  });
});

describe('duration', () => {
  it('reads as minutes, then hours', () => {
    expect([duration(20_000), duration(60_000), duration(35 * 60_000), duration(60 * 60_000), duration(65 * 60_000)])
      .toEqual(['under 1 min', '1 min', '35 min', '1 h', '1 h 5 min']);
  });
});
