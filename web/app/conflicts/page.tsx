import Link from "next/link";
import clsx from "clsx";

import { ConflictList } from "@/components/conflict-list";
import { listConflicts } from "@/lib/api";

export const dynamic = "force-dynamic";

const modes = [
  { label: "Same day", window: undefined, href: "/conflicts" },
  { label: "24 h", window: "24h", href: "/conflicts?window=24h" },
  { label: "48 h", window: "48h", href: "/conflicts?window=48h" },
];

export default async function ConflictsPage({
  searchParams,
}: {
  searchParams: Promise<{ window?: string | string[] }>;
}) {
  const { window } = await searchParams;
  const selected = typeof window === "string" ? window : undefined;

  const { conflicts } = await listConflicts(selected);

  return (
    <>
      <h1 className="mb-4 text-2xl font-semibold text-gray-900">Conflicts</h1>

      <div className="mb-8 flex gap-3">
        {modes.map((mode) => (
          <Link
            key={mode.label}
            href={mode.href}
            className={clsx(
              "rounded border px-3 py-1.5 text-sm",
              mode.window === selected
                ? "border-gray-800 text-gray-900"
                : "border-gray-300 text-gray-600 hover:bg-gray-50",
            )}
          >
            {mode.label}
          </Link>
        ))}
      </div>

      <ConflictList conflicts={conflicts} />
    </>
  );
}
