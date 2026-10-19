import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  outputFileTracingRoot: new URL("../../", import.meta.url).pathname,
  typedRoutes: false,
  experimental: {
    serverActions: { bodySizeLimit: "10mb" },
    authInterrupts: true,
  },
};

export default nextConfig;
