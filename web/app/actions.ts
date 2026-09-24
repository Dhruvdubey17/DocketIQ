"use server";

import { TZDate } from "@date-fns/tz";
import { revalidatePath } from "next/cache";

import * as api from "@/lib/api";
import {
  caseInputSchema,
  deadlineInputSchema,
  type SyncSummary,
} from "@/lib/schemas";

const APP_TIMEZONE = process.env.APP_TIMEZONE ?? "America/New_York";

export type FormState = { error?: string };
export type PollState = { summary?: SyncSummary; error?: string };

// A datetime-local value is wall-clock time with no zone attached, so "17:00"
// has to be read in APP_TIMEZONE rather than the container's UTC. Parsing it as
// UTC first is just a way to split it into fields, which are then rebuilt in
// the app's zone.
function toInstant(local: string): string {
  const wall = new Date(`${local}:00Z`);
  if (Number.isNaN(wall.getTime())) {
    throw new Error(`not a local date and time: ${local}`);
  }
  return new TZDate(
    wall.getUTCFullYear(),
    wall.getUTCMonth(),
    wall.getUTCDate(),
    wall.getUTCHours(),
    wall.getUTCMinutes(),
    0,
    0,
    APP_TIMEZONE,
  ).toISOString();
}

function message(error: unknown): string {
  return error instanceof api.ApiError
    ? error.message
    : "Could not reach the API.";
}

export async function createCaseAction(
  _previous: FormState,
  form: FormData,
): Promise<FormState> {
  const titles = form.getAll("deadline-title").map(String);
  const dues = form.getAll("deadline-due").map(String);

  const parsed = caseInputSchema.safeParse({
    title: form.get("title"),
    // Empty rows are the user leaving a spare one behind, not an error.
    deadlines: titles
      .map((title, i) => ({ title, due: dues[i] ?? "" }))
      .filter((row) => row.title.trim() !== "" || row.due !== ""),
  });
  if (!parsed.success) {
    return { error: parsed.error.issues[0]?.message ?? "Check the form." };
  }

  try {
    await api.createCase({
      title: parsed.data.title,
      deadlines: parsed.data.deadlines.map((row) => ({
        title: row.title,
        due_date: toInstant(row.due),
      })),
    });
  } catch (error) {
    return { error: message(error) };
  }

  revalidatePath("/cases");
  revalidatePath("/");
  revalidatePath("/conflicts");
  return {};
}

export async function addDeadlineAction(form: FormData): Promise<void> {
  const caseId = Number(form.get("case-id"));
  const parsed = deadlineInputSchema.parse({
    title: form.get("title"),
    due: form.get("due"),
  });

  await api.addDeadline(caseId, {
    title: parsed.title,
    due_date: toInstant(parsed.due),
  });

  revalidatePath("/cases");
  revalidatePath("/");
  revalidatePath("/conflicts");
}

export async function deleteDeadlineAction(form: FormData): Promise<void> {
  await api.deleteDeadline(Number(form.get("deadline-id")));

  revalidatePath("/");
  revalidatePath("/cases");
  revalidatePath("/conflicts");
}

export async function pollAction(): Promise<PollState> {
  let summary: SyncSummary;
  try {
    summary = await api.poll();
  } catch (error) {
    return { error: message(error) };
  }

  revalidatePath("/");
  revalidatePath("/cases");
  revalidatePath("/conflicts");
  return { summary };
}
