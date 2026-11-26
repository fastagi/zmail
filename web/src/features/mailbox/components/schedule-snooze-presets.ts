/** Pure local-time preset calculations and formatting helpers for Schedule Send and Snooze. */

export const MIN_SCHEDULE_LEAD_MS = 60_000;
export const MAX_FUTURE_WINDOW_MS = 365 * 24 * 60 * 60 * 1_000;

/** Local `now + 3 hours` with seconds zeroed so the label stays crisp. */
export function laterTodayPreset(now = new Date()): Date {
  const target = new Date(now.getTime() + 3 * 60 * 60 * 1_000);
  target.setSeconds(0, 0);
  return target;
}

/** Tomorrow at the given local wall-clock hour (e.g. 9 for 09:00, 13 for 13:00). */
export function tomorrowAtPreset(hour: number, now = new Date()): Date {
  return new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1, hour, 0, 0, 0);
}

/** Next Monday at the given local wall-clock hour (always at least 1 day ahead). */
export function nextMondayAtPreset(hour: number, now = new Date()): Date {
  const daysUntilMonday = (8 - now.getDay()) % 7 || 7;
  return new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate() + daysUntilMonday,
    hour,
    0,
    0,
    0
  );
}

/** Formats a Date for `<input type="datetime-local">` (`YYYY-MM-DDTHH:mm` in local time). */
export function toDateTimeLocalValue(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/**
 * Validates a `<input type="datetime-local">` string as a future ISO-8601 UTC timestamp within
 * `[now + minLeadMs, now + 365d]`. Returns `null` when empty, unparseable, or out of range.
 */
export function parseCustomFutureIso(
  raw: string,
  minLeadMs: number,
  now = new Date()
): string | null {
  if (!raw.trim()) return null;
  const parsed = new Date(raw);
  const time = parsed.getTime();
  if (Number.isNaN(time)) return null;
  const nowMs = now.getTime();
  if (time < nowMs + minLeadMs || time > nowMs + MAX_FUTURE_WINDOW_MS) return null;
  return parsed.toISOString();
}

/** Compact localized preview shown beside each preset row (e.g. `Nov 27, 09:00`). */
export function formatPresetPreview(date: Date, locale: string, timeZone?: string): string {
  try {
    return new Intl.DateTimeFormat(locale, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
      ...(timeZone ? { timeZone } : {}),
    }).format(date);
  } catch {
    return new Intl.DateTimeFormat(locale, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).format(date);
  }
}
