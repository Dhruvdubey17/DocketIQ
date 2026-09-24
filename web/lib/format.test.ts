import { describe, expect, it } from "vitest";

import {
  dayKey,
  formatDay,
  formatDue,
  formatGap,
  formatTime,
  relativeLabel,
} from "./format";

// 17:00 on 6 October 2026 in New York, written as the UTC instant the API sends.
const fivePmNewYork = "2026-10-06T21:00:00Z";

describe("formatting in APP_TIMEZONE", () => {
  it("renders an instant as New York wall time, not UTC", () => {
    expect(formatDue(fivePmNewYork)).toBe("Oct 6, 2026, 5:00 PM");
    expect(formatTime(fivePmNewYork)).toBe("5:00 PM");
    expect(formatDay(fivePmNewYork)).toBe("Tuesday, October 6");
  });

  it("groups by the local day even when the UTC date has rolled over", () => {
    // 9pm in New York is already tomorrow in UTC.
    expect(dayKey("2026-10-07T01:00:00Z")).toBe("2026-10-06");
    expect(dayKey(fivePmNewYork)).toBe("2026-10-06");
  });
});

describe("relativeLabel", () => {
  it("names the day for anything still due", () => {
    expect(relativeLabel(0, "this_week")).toBe("today");
    expect(relativeLabel(1, "this_week")).toBe("tomorrow");
    expect(relativeLabel(3, "this_week")).toBe("in 3 days");
    expect(relativeLabel(21, "later")).toBe("in 21 days");
  });

  it("says how far past an overdue deadline is", () => {
    expect(relativeLabel(-1, "overdue")).toBe("1 day overdue");
    expect(relativeLabel(-2, "overdue")).toBe("2 days overdue");
  });

  it("calls a deadline that passed today earlier today", () => {
    expect(relativeLabel(0, "overdue")).toBe("earlier today");
  });
});

describe("formatGap", () => {
  it("scales from minutes to days", () => {
    expect(formatGap(45)).toBe("45 min apart");
    expect(formatGap(120)).toBe("2 h apart");
    expect(formatGap(150)).toBe("2 h 30 min apart");
    expect(formatGap(2880)).toBe("2 days apart");
    expect(formatGap(1440)).toBe("1 day apart");
  });
});
