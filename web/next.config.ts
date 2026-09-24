import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Without an explicit root Turbopack walks up past the repository and picks
  // the first lockfile it finds, which may be one in the home directory.
  turbopack: { root: import.meta.dirname },
};

export default nextConfig;
