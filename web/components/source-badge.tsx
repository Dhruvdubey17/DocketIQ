import type { Deadline } from "@/lib/schemas";

const labels: Record<Deadline["source"], string> = {
  manual: "Manual",
  docket: "Docket feed",
  calendar: "Calendar feed",
};

export function SourceBadge({ source }: { source: Deadline["source"] }) {
  return (
    <span className="rounded border border-gray-300 px-1.5 py-0.5 text-xs text-gray-600">
      {labels[source]}
    </span>
  );
}
