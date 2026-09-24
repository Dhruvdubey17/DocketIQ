import { dayKey, formatDay, formatGap, formatTime } from "@/lib/format";
import type { Conflict } from "@/lib/schemas";

function groupByDay(conflicts: Conflict[]) {
  const groups = new Map<string, Conflict[]>();
  for (const conflict of conflicts) {
    const key = dayKey(conflict.a.due_date);
    groups.set(key, [...(groups.get(key) ?? []), conflict]);
  }
  return [...groups.entries()];
}

export function ConflictList({ conflicts }: { conflicts: Conflict[] }) {
  if (conflicts.length === 0) {
    return <p className="text-sm text-gray-500">No conflicts.</p>;
  }

  return (
    <div className="space-y-8">
      {groupByDay(conflicts).map(([key, group]) => (
        <section key={key}>
          <h2 className="mb-3 text-lg font-semibold text-gray-900">
            {formatDay(group[0]!.a.due_date)}
          </h2>

          <ul className="space-y-3">
            {group.map((conflict) => (
              <li
                key={`${conflict.a.id}-${conflict.b.id}`}
                className="border-l-4 border-l-amber-500 bg-white py-3 pl-4"
              >
                <div className="grid gap-1 sm:grid-cols-2">
                  {[conflict.a, conflict.b].map((deadline) => (
                    <div key={deadline.id}>
                      <p className="font-medium text-gray-900">
                        {deadline.title}
                      </p>
                      <p className="text-sm text-gray-600">
                        {deadline.case_title}
                      </p>
                      <p className="text-sm text-gray-500">
                        {formatTime(deadline.due_date)}
                      </p>
                    </div>
                  ))}
                </div>
                <p className="mt-2 text-sm text-gray-500">
                  {formatGap(conflict.gap_minutes)}
                </p>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
