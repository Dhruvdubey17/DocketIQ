import clsx from "clsx";

import type { Bucket } from "@/lib/schemas";

// Colour alone would leave the state unreadable to anyone who can't see the
// difference, so every badge carries the word too.
const labels: Record<Bucket, string> = {
  overdue: "Overdue",
  this_week: "This week",
  later: "Later",
};

const dots: Record<Bucket, string> = {
  overdue: "bg-red-500",
  this_week: "bg-amber-500",
  later: "bg-gray-400",
};

export function BucketBadge({ bucket }: { bucket: Bucket }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-sm text-gray-600">
      <span
        className={clsx("h-2 w-2 rounded-full", dots[bucket])}
        aria-hidden="true"
      />
      {labels[bucket]}
    </span>
  );
}

export const bucketLabels = labels;
