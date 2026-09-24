import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // The runtime image copies .next/standalone and runs its server.js, so the
  // build has to trace its own dependencies rather than expect node_modules.
  output: "standalone",

  // Without an explicit root Turbopack walks up past the repository and picks
  // the first lockfile it finds, which may be one in the home directory.
  turbopack: { root: import.meta.dirname },
};

export default nextConfig;
