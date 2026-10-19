import type { Metadata, Viewport } from "next";
import { Montserrat } from "next/font/google";

import { SessionProvider } from "@/components/session-provider";
import { SiteHeader } from "@/components/site-header";
import { Toaster } from "@/components/ui/sonner";
import { readIdentity } from "@/lib/session";

import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Karmarat Jobs — Find your next role",
    template: "%s · Karmarat Jobs",
  },
  description:
    "Search open roles across every company hiring on Karmarat, apply with one profile, and follow every application from submission to offer.",
  applicationName: "Karmarat Jobs",
  icons: { icon: "/logo.png", shortcut: "/logo.png", apple: "/logo.png" },
};

const font = Montserrat({
  subsets: ["latin"],
  variable: "--font-sans",
  display: "swap",
});

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#4B352A" },
    { media: "(prefers-color-scheme: dark)", color: "#1a1512" },
  ],
};

export default async function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // The identity cookie carries no token, so reading it here costs nothing and
  // rotates nothing. It decides what this shell renders; the gateway decides
  // what the account may actually do.
  const identity = await readIdentity();

  return (
    <html lang="en" className={font.variable} suppressHydrationWarning>
      <body className={`${font.className} ${font.variable} min-h-dvh`}>
        <SessionProvider expectSession={identity !== null}>
          <a
            href="#main"
            className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-50 focus:rounded-xs focus:bg-primary focus:px-3 focus:py-2 focus:text-sm focus:text-primary-foreground"
          >
            Skip to content
          </a>
          <SiteHeader identity={identity} />
          <main id="main" className="mx-auto w-full max-w-6xl px-4 py-8">
            {children}
          </main>
          <footer className="border-t border-border">
            <div className="mx-auto w-full max-w-6xl px-4 py-6 text-xs text-muted-foreground">
              Karmarat — one profile, every company hiring on the network.
            </div>
          </footer>
        </SessionProvider>
        <Toaster position="top-right" />
      </body>
    </html>
  );
}
