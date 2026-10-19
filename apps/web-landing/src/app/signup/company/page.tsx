import type { Metadata } from "next";
import Link from "next/link";

import { Container } from "@/components/section";
import { PORTAL_HOST } from "@/lib/env";
import { CompanySignUpForm } from "./company-signup-form";

export const metadata: Metadata = {
  title: "Create a company account",
  description:
    "Register your company on Karmarat, choose your careers-portal address, and create the owner account.",
};

/**
 * The form itself is a client component because the address field answers as
 * you type. Everything around it is server-rendered, including the hostname —
 * which comes from configuration rather than from `window.location`, so the
 * preview reads the same before hydration as after.
 */
export default function CompanySignUpPage() {
  return (
    <Container className="grid gap-12 py-12 sm:py-16 lg:grid-cols-[minmax(0,1fr)_320px]">
      <div className="min-w-0">
        <h1 className="text-3xl">Create a company account</h1>
        <p className="mt-3 max-w-xl text-muted-foreground">
          Two minutes. You pick your address, create the account that owns the
          company, and we take it from there.
        </p>

        <div className="mt-10 max-w-xl">
          <CompanySignUpForm portalHost={PORTAL_HOST} />
        </div>
      </div>

      <aside className="lg:pt-24">
        <div className="rounded-xs border border-border bg-muted/40 p-6 text-sm">
          <h2 className="text-base">What happens next</h2>
          <ol className="mt-4 flex list-decimal flex-col gap-3 pl-4 text-muted-foreground">
            <li>Your company is created and held for review by the platform team.</li>
            <li>
              Once approved, your portal opens at{" "}
              <span className="tabular">your-address.{PORTAL_HOST}</span>.
            </li>
            <li>You sign in there, choose a plan, and invite your team.</li>
          </ol>

          <hr className="my-6 border-border" />

          <p className="text-muted-foreground">
            Looking for a job rather than hiring?{" "}
            <Link href="/for-candidates" className="underline underline-offset-4">
              Candidate accounts are separate and free.
            </Link>
          </p>
        </div>
      </aside>
    </Container>
  );
}
