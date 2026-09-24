import type { Bucket, Deadline } from "@/lib/schemas";

import { bucketLabels } from "./bucket-badge";
import { DeadlineRow } from "./deadline-row";

const emptyMessages: Record<Bucket, string> = {
  overdue: "Nothing overdue.",
  this_week: "Nothing due this week.",
  later: "Nothing further out.",
};

export function DeadlineSection({
  bucket,
  deadlines,
}: {
  bucket: Bucket;
  deadlines: Deadline[];
}) {
  return (
    <section className="mb-8">
      <h2 className="mb-3 text-lg font-semibold text-gray-900">
        {bucketLabels[bucket]}
        <span className="ml-2 text-sm font-normal text-gray-500">
          {deadlines.length}
        </span>
      </h2>

      {deadlines.length === 0 ? (
        <p className="text-sm text-gray-500">{emptyMessages[bucket]}</p>
      ) : (
        <ul className="space-y-2">
          {deadlines.map((deadline) => (
            <DeadlineRow key={deadline.id} deadline={deadline} />
          ))}
        </ul>
      )}
    </section>
  );
}
