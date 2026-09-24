import "server-only";
import type { z } from "zod";

import {
  apiErrorSchema,
  casesResponseSchema,
  conflictsResponseSchema,
  createCaseResponseSchema,
  deadlinesResponseSchema,
  deadlineSchema,
  syncSummarySchema,
} from "./schemas";

// ApiError carries the API's own message, which is written to be shown.
export class ApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

type Body = { title: string; due_date: string };

async function send(path: string, init?: RequestInit): Promise<Response> {
  const base = process.env.API_URL ?? "http://api:8080";
  const response = await fetch(`${base}${path}`, {
    ...init,
    cache: "no-store",
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  if (response.ok) {
    return response;
  }

  const parsed = apiErrorSchema.safeParse(
    await response.json().catch(() => null),
  );
  throw new ApiError(
    parsed.success ? parsed.data.error : `the API answered ${response.status}`,
    response.status,
  );
}

async function get<T>(path: string, schema: z.ZodType<T>): Promise<T> {
  return schema.parse(await (await send(path)).json());
}

export async function listCases() {
  return get("/api/cases", casesResponseSchema);
}

export async function listDeadlines(sort: "due" | "urgency") {
  return get(`/api/deadlines?sort=${sort}`, deadlinesResponseSchema);
}

export async function listConflicts(window?: string) {
  const query = window ? `?window=${encodeURIComponent(window)}` : "";
  return get(`/api/deadlines/conflicts${query}`, conflictsResponseSchema);
}

export async function createCase(body: { title: string; deadlines: Body[] }) {
  const response = await send("/api/cases", {
    method: "POST",
    body: JSON.stringify(body),
  });
  return createCaseResponseSchema.parse(await response.json());
}

export async function addDeadline(caseId: number, body: Body) {
  const response = await send(`/api/cases/${caseId}/deadlines`, {
    method: "POST",
    body: JSON.stringify(body),
  });
  return deadlineSchema.parse(await response.json());
}

export async function deleteDeadline(id: number) {
  await send(`/api/deadlines/${id}`, { method: "DELETE" });
}

export async function poll() {
  const response = await send("/api/poll", { method: "POST" });
  return syncSummarySchema.parse(await response.json());
}
