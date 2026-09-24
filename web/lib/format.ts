import type { Bucket } from "./schemas";

const APP_TIMEZONE = process.env.APP_TIMEZONE ?? "America/New_York";

// The server runs in UTC. Without an explicit zone every date would render
// a few hours off.
const dueFormat = new Intl.DateTimeFormat("en-US", {
  timeZone: APP_TIMEZONE,
  dateStyle: "medium",
  timeStyle: "short",
});

const dayFormat = new Intl.DateTimeFormat("en-US", {
  timeZone: APP_TIMEZONE,
  weekday: "long",
  month: "long",
  day: "numeric",
});

const timeFormat = new Intl.DateTimeFormat("en-US", {
  timeZone: APP_TIMEZONE,
  timeStyle: "short",
});

// en-CA gives YYYY-MM-DD, which sorts and compares as a plain string.
const dayKeyFormat = new Intl.DateTimeFormat("en-CA", {
  timeZone: APP_TIMEZONE,
});

export function formatDue(iso: string): string {
  return dueFormat.format(new Date(iso));
}

export function formatTime(iso: string): string {
  return timeFormat.format(new Date(iso));
}

export function formatDay(iso: string): string {
  return dayFormat.format(new Date(iso));
}

// Grouping keys off the local date, not the UTC one, or a late-evening
// deadline ends up under tomorrow's heading.
export function dayKey(iso: string): string {
  return dayKeyFormat.format(new Date(iso));
}

export function relativeLabel(daysUntilDue: number, bucket: Bucket): string {
  if (bucket === "overdue") {
    if (daysUntilDue >= 0) {
      return "earlier today";
    }
    const days = Math.abs(daysUntilDue);
    return `${days} ${days === 1 ? "day" : "days"} overdue`;
  }
  if (daysUntilDue === 0) {
    return "today";
  }
  if (daysUntilDue === 1) {
    return "tomorrow";
  }
  return `in ${daysUntilDue} days`;
}

export function formatGap(minutes: number): string {
  if (minutes < 60) {
    return `${minutes} min apart`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    const rest = minutes % 60;
    return rest === 0 ? `${hours} h apart` : `${hours} h ${rest} min apart`;
  }
  const days = Math.round(hours / 24);
  return `${days} ${days === 1 ? "day" : "days"} apart`;
}
