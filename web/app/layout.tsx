import type { Metadata } from "next";

import { Nav } from "@/components/nav";

import "./globals.css";

export const metadata: Metadata = {
  title: "DocketIQ",
  description: "Case and deadline tracker",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className="h-full antialiased">
      <body className="mx-auto flex min-h-full max-w-4xl flex-col p-6">
        <Nav />
        <main>{children}</main>
      </body>
    </html>
  );
}
