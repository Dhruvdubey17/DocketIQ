import { z } from "zod";

export const bucketSchema = z.enum(["overdue", "this_week", "later"]);
export type Bucket = z.infer<typeof bucketSchema>;

export const deadlineSchema = z.object({
  id: z.number().int(),
  case_id: z.number().int(),
  case_title: z.string(),
  title: z.string(),
  due_date: z.string(),
  source: z.enum(["manual", "docket", "calendar"]),
  created_at: z.string(),
  days_until_due: z.number().int(),
  urgency: z.number(),
  bucket: bucketSchema,
});
export type Deadline = z.infer<typeof deadlineSchema>;

export const caseSchema = z.object({
  id: z.number().int(),
  title: z.string(),
  created_at: z.string(),
  deadline_count: z.number().int(),
});
export type Case = z.infer<typeof caseSchema>;

export const casesResponseSchema = z.object({ cases: z.array(caseSchema) });
export const deadlinesResponseSchema = z.object({
  deadlines: z.array(deadlineSchema),
});

export const conflictSchema = z.object({
  a: deadlineSchema,
  b: deadlineSchema,
  gap_minutes: z.number().int(),
});
export type Conflict = z.infer<typeof conflictSchema>;

export const conflictsResponseSchema = z.object({
  mode: z.enum(["same_day", "window"]),
  window_minutes: z.number().int().optional(),
  conflicts: z.array(conflictSchema),
});
export type ConflictsResponse = z.infer<typeof conflictsResponseSchema>;

export const createCaseResponseSchema = z.object({
  case: caseSchema,
  deadlines: z.array(deadlineSchema),
});

export const syncSummarySchema = z.object({
  started_at: z.string(),
  duration_ms: z.number(),
  sources: z.number(),
  fetched: z.number(),
  shared: z.number(),
  cache_hits: z.number(),
  items: z.number(),
  inserted: z.number(),
  updated: z.number(),
  unchanged: z.number(),
  errors: z.array(z.object({ source: z.string(), error: z.string() })),
});
export type SyncSummary = z.infer<typeof syncSummarySchema>;

export const apiErrorSchema = z.object({ error: z.string() });

// The API trims and limits titles the same way. Catching it here means the
// form can say so without a round trip.
const titleSchema = z
  .string()
  .trim()
  .min(1, "Title is required")
  .max(200, "Title must be at most 200 characters");

// A datetime-local input carries no zone, which is why the action reads it in
// APP_TIMEZONE before sending it on.
const localDateTimeSchema = z
  .string()
  .regex(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/, "Give a date and a time");

export const deadlineInputSchema = z.object({
  title: titleSchema,
  due: localDateTimeSchema,
});
export type DeadlineInput = z.infer<typeof deadlineInputSchema>;

export const caseInputSchema = z.object({
  title: titleSchema,
  deadlines: z
    .array(deadlineInputSchema)
    .max(20, "At most 20 deadlines per case"),
});
