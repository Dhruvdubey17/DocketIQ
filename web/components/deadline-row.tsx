import clsx from "clsx";

import { deleteDeadlineAction } from "@/app/actions";
import { formatDue, relativeLabel } from "@/lib/format";
import type { Bucket, Deadline } from "@/lib/schemas";

import { SourceBadge } from "./source-badge";

const borders: Record<Bucket, string> = {
  overdue: "border-l-red-500",
  this_week: "border-l-amber-500",
  later: "border-l-gray-300",
};

export function DeadlineRow({ deadline }: { deadline: Deadline }) {
  return (
    <li
      className={clsx(
        "flex items-start justify-between gap-4 border-l-4 bg-white py-3 pl-4",
        borders[deadline.bucket],
      )}
    >
      <div>
        <p className="font-medium text-gray-900">{deadline.title}</p>
        <p className="text-sm text-gray-600">{deadline.case_title}</p>
        <p className="mt-1 text-sm text-gray-500">
          {formatDue(deadline.due_date)}
          <span className="mx-2 text-gray-300">|</span>
          {relativeLabel(deadline.days_until_due, deadline.bucket)}
        </p>
      </div>

      <div className="flex shrink-0 items-center gap-3">
        <SourceBadge source={deadline.source} />
        {deadline.source === "manual" && (
          <form action={deleteDeadlineAction}>
            <input type="hidden" name="deadline-id" value={deadline.id} />
            <button
              type="submit"
              className="text-sm text-gray-500 hover:text-red-600 hover:underline"
            >
              Delete
            </button>
          </form>
        )}
      </div>
    </li>
  );
}
