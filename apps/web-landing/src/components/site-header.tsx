import Image from "next/image";
import Link from "next/link";

import { Button } from "@/components/ui/button";
import { JOBS_PORTAL_URL } from "@/lib/env";

/**
 * No hamburger, no client JavaScript.
 *
 * There are four links. A disclosure menu would cost a client bundle on every
 * page of an otherwise static site to hide four items that fit on two rows.
 */
export function SiteHeader() {
  return (
    <header className="sticky top-0 z-40 border-b border-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/80">
      <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-6 gap-y-3 px-4 py-3 sm:px-6">
        <Link
          href="/"
          className="flex shrink-0 items-center gap-2 font-semibold tracking-tight"
        >
          <Image src="/logo.png" alt="" width={28} height={28} className="h-7 w-7 object-contain" />
          <span>Karmarat</span>
        </Link>

        <nav aria-label="Main" className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
          <Link href="/for-companies" className="text-muted-foreground hover:text-foreground">
            For companies
          </Link>
          <Link href="/for-candidates" className="text-muted-foreground hover:text-foreground">
            For candidates
          </Link>
          <Link href="/pricing" className="text-muted-foreground hover:text-foreground">
            Pricing
          </Link>
          <a href={JOBS_PORTAL_URL} className="text-muted-foreground hover:text-foreground">
            Find a job
          </a>
        </nav>

        <div className="ml-auto flex items-center gap-2">
          <Button asChild variant="ghost" size="sm">
            <Link href="/signin">Sign in</Link>
          </Button>
          <Button asChild variant="accent" size="sm">
            <Link href="/signup/company">Create a company account</Link>
          </Button>
        </div>
      </div>
    </header>
  );
}
