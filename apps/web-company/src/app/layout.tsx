import type { Metadata, Viewport } from "next";
import NextTopLoader from "nextjs-toploader";

import { Toaster } from "@/components/ui/sonner";
import { Montserrat } from "next/font/google";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Karmarat — Modern Recruitment & Talent Platform",
    template: "%s · Karmarat",
  },
  description:
    "Recruitment management for high-growth teams: candidate pipelines, job requisitions, interviews, evaluations, offers, and talent analytics in one cohesive system.",
  applicationName: "Karmarat",
  icons: {
    icon: "/logo.png",
    shortcut: "/logo.png",
    apple: "/logo.png",
  },
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

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className={font.variable} suppressHydrationWarning>
      <body className={`${font.className} ${font.variable}`} suppressHydrationWarning>
        <NextTopLoader
          color="#CA7842"
          initialPosition={0.08}
          crawlSpeed={200}
          height={3}
          crawl={true}
          showSpinner={false}
          easing="ease"
          speed={200}
          shadow="0 0 10px #CA7842,0 0 5px #CA7842"
        />
        {children}
        <Toaster position="top-right" />
      </body>
    </html>
  );
}
