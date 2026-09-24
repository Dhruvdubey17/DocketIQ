"use client";

import { useActionState } from "react";

import { pollAction, type PollState } from "@/app/actions";
import type { SyncSummary } from "@/lib/schemas";

function summaryLine(summary: SyncSummary): string {
  return [
    `${summary.sources} sources`,
    `${summary.fetched} fetched`,
    `${summary.shared} shared`,
    `${summary.cache_hits} cached`,
    `${summary.duration_ms} ms`,
  ].join(" · ");
}

export function PollButton() {
  const [state, formAction, pending] = useActionState<PollState, FormData>(
    async () => pollAction(),
    {},
  );

  return (
    <form action={formAction}>
      <button
        type="submit"
        disabled={pending}
        className="rounded border border-gray-300 px-3 py-1.5 text-sm text-gray-800 hover:bg-gray-50 disabled:opacity-50"
      >
        {pending ? "Polling..." : "Poll sources"}
      </button>

      {state.summary && (
        <p className="mt-2 text-sm text-gray-600">
          {summaryLine(state.summary)}
        </p>
      )}

      {state.summary?.errors.map((failure) => (
        <p key={failure.source} className="mt-1 text-sm text-red-700">
          {failure.source}: {failure.error}
        </p>
      ))}

      {state.error && (
        <p className="mt-2 text-sm text-red-700">{state.error}</p>
      )}
    </form>
  );
}
