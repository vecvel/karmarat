import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  outputFileTracingRoot: new URL("../../", import.meta.url).pathname,
  typedRoutes: false,
  // The shared platform layer is published as compiled JS with declarations, so
  // it needs no transpilation here — it is listed only so a change to it during
  // development invalidates this app's build cache.
  transpilePackages: [],
  experimental: {
    authInterrupts: true,
  },
};

export default nextConfig;
