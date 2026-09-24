import { addDeadlineAction } from "@/app/actions";

export function AddDeadlineForm({ caseId }: { caseId: number }) {
  return (
    <form action={addDeadlineAction} className="mt-2 flex gap-2">
      <input type="hidden" name="case-id" value={caseId} />
      <input
        name="title"
        required
        maxLength={200}
        placeholder="Deadline title"
        className="w-full rounded border border-gray-300 px-2 py-1.5 text-sm"
      />
      <input
        name="due"
        type="datetime-local"
        required
        className="rounded border border-gray-300 px-2 py-1.5 text-sm"
      />
      <button
        type="submit"
        className="shrink-0 rounded border border-gray-300 px-3 py-1.5 text-sm text-gray-800 hover:bg-gray-50"
      >
        Add
      </button>
    </form>
  );
}
