import { Children } from "react";
import Link from "next/link";

import { JOBS_PORTAL_URL, PORTAL_HOST, ROOT_PORTAL_URL } from "@/lib/env";

export function SiteFooter() {
  const year = new Date().getFullYear();

  return (
    <footer className="mt-20 border-t border-border bg-muted/40">
      <div className="mx-auto grid max-w-6xl gap-10 px-4 py-12 sm:px-6 md:grid-cols-4">
        <div className="md:col-span-1">
          <p className="font-semibold tracking-tight">Karmarat</p>
          <p className="mt-2 text-sm text-muted-foreground">
            Recruitment software for companies, and one job board for the candidates
            who apply through it.
          </p>
        </div>

        <FooterColumn title="Companies">
          <Link href="/for-companies">What you get</Link>
          <Link href="/pricing">Pricing</Link>
          <Link href="/signup/company">Create an account</Link>
        </FooterColumn>

        <FooterColumn title="Candidates">
          <Link href="/for-candidates">How it works</Link>
          <a href={JOBS_PORTAL_URL}>Find a job</a>
          <a href={`${JOBS_PORTAL_URL}/register`}>Create a candidate account</a>
        </FooterColumn>

        <FooterColumn title="Company">
          <Link href="/signin">Sign in</Link>
          <Link href="/legal/terms">Terms</Link>
          <Link href="/legal/privacy">Privacy</Link>
          <a href={ROOT_PORTAL_URL}>Platform console</a>
        </FooterColumn>
      </div>

      <div className="border-t border-border">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center justify-between gap-2 px-4 py-4 text-xs text-muted-foreground sm:px-6">
          <p>© {year} Karmarat</p>
          <p className="tabular">{PORTAL_HOST}</p>
        </div>
      </div>
    </footer>
  );
}

function FooterColumn({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <nav aria-label={title}>
      <p className="section-title">{title}</p>
      <ul className="mt-3 flex flex-col gap-2 text-sm [&_a]:text-muted-foreground [&_a:hover]:text-foreground">
        {/* Each child is one link; wrapping here keeps the list semantics
            without every call site repeating an <li>. */}
        {Children.toArray(children).map((child, index) => (
          <li key={index}>{child}</li>
        ))}
      </ul>
    </nav>
  );
}
