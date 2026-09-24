import type { Metadata } from "next";
import type { ReactNode } from "react";

import { Nav } from "@/components/nav";

import "./globals.css";

export const metadata: Metadata = {
  title: "DocketIQ",
  description: "Case and deadline tracker",
};

// LayoutProps is generated into .next/types during a build, so depending on it
// makes `tsc --noEmit` pass or fail based on whether a build ran first.
export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en" className="h-full antialiased">
      <body className="mx-auto flex min-h-full max-w-4xl flex-col p-6">
        <Nav />
        <main>{children}</main>
      </body>
    </html>
  );
}
