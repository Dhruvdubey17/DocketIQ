import Link from "next/link";

const links = [
  { href: "/", label: "Dashboard" },
  { href: "/conflicts", label: "Conflicts" },
  { href: "/cases", label: "Cases" },
];

export function Nav() {
  return (
    <nav className="mb-8 flex gap-4 border-b border-gray-200 pb-3">
      {links.map((link) => (
        <Link
          key={link.href}
          href={link.href}
          className="text-sm text-gray-700 hover:underline"
        >
          {link.label}
        </Link>
      ))}
    </nav>
  );
}
