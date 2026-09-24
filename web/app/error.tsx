"use client";

export default function ErrorBoundary({
  reset,
}: {
  error: Error;
  reset: () => void;
}) {
  return (
    <div className="rounded border border-gray-200 p-6">
      <h1 className="mb-2 text-lg font-semibold text-gray-900">
        Something went wrong
      </h1>
      <p className="mb-4 text-sm text-gray-600">
        The API did not answer. Check that it is running, then try again.
      </p>
      <button
        type="button"
        onClick={reset}
        className="rounded border border-gray-300 px-3 py-1.5 text-sm text-gray-800 hover:bg-gray-50"
      >
        Try again
      </button>
    </div>
  );
}
