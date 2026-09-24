"use client";

import { useActionState, useState } from "react";

import { createCaseAction, type FormState } from "@/app/actions";

const inputStyle = "w-full rounded border border-gray-300 px-2 py-1.5 text-sm";

export function CaseForm() {
  const [rowIds, setRowIds] = useState([0]);
  const [state, formAction, pending] = useActionState<FormState, FormData>(
    createCaseAction,
    {},
  );

  return (
    <form
      action={formAction}
      className="mb-10 rounded border border-gray-200 p-4"
    >
      <h2 className="mb-3 text-lg font-semibold text-gray-900">New case</h2>

      <label className="mb-3 block">
        <span className="mb-1 block text-sm text-gray-700">Case title</span>
        <input name="title" required maxLength={200} className={inputStyle} />
      </label>

      <fieldset className="mb-3">
        <legend className="mb-1 text-sm text-gray-700">Deadlines</legend>

        {rowIds.map((id) => (
          <div key={id} className="mb-2 flex gap-2">
            <input
              name="deadline-title"
              maxLength={200}
              placeholder="Deadline title"
              className={inputStyle}
            />
            <input
              name="deadline-due"
              type="datetime-local"
              className={inputStyle}
            />
            <button
              type="button"
              onClick={() =>
                setRowIds((ids) =>
                  ids.length > 1 ? ids.filter((x) => x !== id) : ids,
                )
              }
              className="shrink-0 px-2 text-sm text-gray-500 hover:text-red-600"
            >
              Remove
            </button>
          </div>
        ))}

        <button
          type="button"
          onClick={() => setRowIds((ids) => [...ids, Math.max(...ids) + 1])}
          className="text-sm text-gray-600 hover:underline"
        >
          Add another deadline
        </button>
      </fieldset>

      <button
        type="submit"
        disabled={pending}
        className="rounded border border-gray-300 px-3 py-1.5 text-sm text-gray-800 hover:bg-gray-50 disabled:opacity-50"
      >
        {pending ? "Creating..." : "Create case"}
      </button>

      {state.error && (
        <p className="mt-2 text-sm text-red-700">{state.error}</p>
      )}
    </form>
  );
}
