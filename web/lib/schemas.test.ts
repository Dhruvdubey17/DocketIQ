import { describe, expect, it } from "vitest";

import {
  caseInputSchema,
  casesResponseSchema,
  conflictsResponseSchema,
  deadlineSchema,
  syncSummarySchema,
} from "./schemas";

// Every payload below is copied from SCOPE section 9.
const deadline = {
  id: 42,
  case_id: 1,
  case_title: "Harlow v. Brightline Freight",
  title: "Opposition to motion to dismiss due",
  due_date: "2026-10-06T21:00:00Z",
  source: "docket",
  created_at: "2026-09-22T13:05:11Z",
  days_until_due: 14,
  urgency: 0.0667,
  bucket: "later",
};

describe("response schemas", () => {
  it("parses a deadline", () => {
    expect(deadlineSchema.parse(deadline).id).toBe(42);
  });

  it("rejects a bucket the UI has no colour for", () => {
    expect(() =>
      deadlineSchema.parse({ ...deadline, bucket: "someday" }),
    ).toThrow();
  });

  it("parses a case list", () => {
    const parsed = casesResponseSchema.parse({
      cases: [
        {
          id: 1,
          title: "Harlow v. Brightline Freight",
          created_at: "2026-09-20T14:02:11Z",
          deadline_count: 5,
        },
      ],
    });
    expect(parsed.cases[0]?.deadline_count).toBe(5);
  });

  it("parses both conflict modes", () => {
    const sameDay = conflictsResponseSchema.parse({
      mode: "same_day",
      conflicts: [{ a: deadline, b: deadline, gap_minutes: 45 }],
    });
    expect(sameDay.window_minutes).toBeUndefined();

    const windowed = conflictsResponseSchema.parse({
      mode: "window",
      window_minutes: 2880,
      conflicts: [],
    });
    expect(windowed.window_minutes).toBe(2880);
  });

  it("parses a poll summary", () => {
    const parsed = syncSummarySchema.parse({
      started_at: "2026-09-22T14:00:03Z",
      duration_ms: 861,
      sources: 8,
      fetched: 7,
      shared: 1,
      cache_hits: 0,
      items: 26,
      inserted: 26,
      updated: 0,
      unchanged: 0,
      errors: [],
    });
    expect(parsed.shared).toBe(1);
  });
});

describe("form input schemas", () => {
  it("trims a title and rejects a blank one", () => {
    expect(
      caseInputSchema.parse({
        title: "  Mendez v. City of Arden  ",
        deadlines: [],
      }).title,
    ).toBe("Mendez v. City of Arden");
    expect(() =>
      caseInputSchema.parse({ title: "   ", deadlines: [] }),
    ).toThrow();
  });

  it("rejects a title over 200 characters", () => {
    expect(() =>
      caseInputSchema.parse({ title: "x".repeat(201), deadlines: [] }),
    ).toThrow();
  });

  it("requires a datetime-local shaped due value", () => {
    const valid = { title: "Initial disclosures due", due: "2026-10-02T17:00" };
    expect(
      caseInputSchema.parse({ title: "case", deadlines: [valid] }).deadlines,
    ).toHaveLength(1);
    expect(() =>
      caseInputSchema.parse({
        title: "case",
        deadlines: [{ ...valid, due: "2026-10-02" }],
      }),
    ).toThrow();
  });

  it("caps a case at 20 deadlines", () => {
    const rows = Array.from({ length: 21 }, () => ({
      title: "filing",
      due: "2026-10-02T17:00",
    }));
    expect(() =>
      caseInputSchema.parse({ title: "case", deadlines: rows }),
    ).toThrow();
  });
});
