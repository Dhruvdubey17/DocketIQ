import { AddDeadlineForm } from "@/components/add-deadline-form";
import { CaseForm } from "@/components/case-form";
import { listCases } from "@/lib/api";

export const dynamic = "force-dynamic";

export default async function CasesPage() {
  const { cases } = await listCases();

  return (
    <>
      <h1 className="mb-4 text-2xl font-semibold text-gray-900">Cases</h1>

      <CaseForm />

      <ul className="space-y-3">
        {cases.map((item) => (
          <li
            key={item.id}
            className="border-l-4 border-l-gray-300 bg-white py-3 pl-4"
          >
            <p className="font-medium text-gray-900">{item.title}</p>
            <p className="text-sm text-gray-600">
              {item.deadline_count}{" "}
              {item.deadline_count === 1 ? "deadline" : "deadlines"}
            </p>

            <details className="mt-2">
              <summary className="cursor-pointer text-sm text-gray-600">
                Add deadline
              </summary>
              <AddDeadlineForm caseId={item.id} />
            </details>
          </li>
        ))}
      </ul>
    </>
  );
}
