import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  outputFileTracingRoot: new URL("../../", import.meta.url).pathname,
  typedRoutes: false,
  // This app holds no session and no database client. Everything it renders
  // comes from the gateway over HTTP, so there is nothing to externalise.
  experimental: {
    serverActions: {
      // The registration form is text only; a generous body limit here would
      // just widen what an unauthenticated endpoint accepts.
      bodySizeLimit: "64kb",
    },
  },
};

export default nextConfig;
