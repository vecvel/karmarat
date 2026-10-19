import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  // The console has no database client and no Node-only dependency to exclude;
  // the only thing it talks to is the gateway.
  typedRoutes: false,
  experimental: {
    authInterrupts: true,
  },
  // The shared platform layer ships as compiled JS in packages/ui/dist, so
  // nothing here needs transpiling — but the workspace symlink means Next has
  // to be told the monorepo root or it infers the wrong one for file tracing.
  outputFileTracingRoot: new URL("../../", import.meta.url).pathname,
};

export default nextConfig;
