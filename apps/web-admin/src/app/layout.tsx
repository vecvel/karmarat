import type { Metadata, Viewport } from "next";
import { Montserrat } from "next/font/google";

import { Toaster } from "@/components/ui/sonner";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Platform Console · Karmarat",
    template: "%s · Platform Console",
  },
  description:
    "Karmarat platform administration: tenants, plans, subscriptions, payments, support and platform health.",
  applicationName: "Karmarat Platform Console",
  // Nothing in this console should ever reach a search index.
  robots: { index: false, follow: false },
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

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={font.variable} suppressHydrationWarning>
      <body className={`${font.className} ${font.variable}`} suppressHydrationWarning>
        {children}
        <Toaster position="top-right" />
      </body>
    </html>
  );
}
