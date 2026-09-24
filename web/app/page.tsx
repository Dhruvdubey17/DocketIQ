import { PollButton } from "@/components/poll-button";
import { DeadlineSection } from "@/components/deadline-section";
import { listDeadlines } from "@/lib/api";
import type { Bucket, Deadline } from "@/lib/schemas";

// next build runs without the API, so nothing here may be prerendered.
export const dynamic = "force-dynamic";

const order: Bucket[] = ["overdue", "this_week", "later"];

export default async function DashboardPage() {
  const { deadlines } = await listDeadlines("due");

  const byBucket = new Map<Bucket, Deadline[]>(
    order.map((bucket) => [bucket, []]),
  );
  for (const deadline of deadlines) {
    byBucket.get(deadline.bucket)?.push(deadline);
  }

  return (
    <>
      <header className="mb-8 flex items-start justify-between gap-4">
        <h1 className="text-2xl font-semibold text-gray-900">Deadlines</h1>
        <PollButton />
      </header>

      {order.map((bucket) => (
        <DeadlineSection
          key={bucket}
          bucket={bucket}
          deadlines={byBucket.get(bucket) ?? []}
        />
      ))}
    </>
  );
}
