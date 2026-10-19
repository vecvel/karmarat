import { Suspense } from "react";
import type { Metadata } from "next";
import Link from "next/link";

import { Container, Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { PlanGrid, PlanGridSkeleton } from "./plan-grid";

export const metadata: Metadata = {
  title: "Pricing",
  description:
    "What a Karmarat company account costs, and exactly what each plan includes.",
};

/**
 * Plans come from the gateway on every render window, never from this file.
 *
 * The catalogue is edited by the platform team in the admin console. A plan
 * hard-coded here would be a price this page shows and the billing service does
 * not honour, which is the worst kind of wrong on a pricing page.
 */
export default function PricingPage() {
  return (
    <>
      <section className="border-b border-border bg-parchment-soft dark:bg-secondary">
        <Container className="py-12 sm:py-16">
          <h1 className="text-3xl sm:text-4xl">Pricing</h1>
          <p className="mt-3 max-w-2xl text-muted-foreground">
            One subscription per company, covering everyone on your team. Candidate
            accounts are free and always will be — candidates are not the customer here.
          </p>
        </Container>
      </section>

      <Section>
        {/* The plan list is the only thing on this page that waits on the
            network, so it is the only thing behind a boundary. The heading and
            the copy above render immediately. */}
        <Suspense fallback={<PlanGridSkeleton />}>
          <PlanGrid />
        </Suspense>
      </Section>

      <Section className="border-t border-border bg-muted/40" title="Questions people actually ask">
        <dl className="grid gap-8 sm:grid-cols-2">
          <Faq q="What happens when a subscription lapses?">
            Your portal closes to new activity and your team is signed out — the
            entitlement is checked on every request, not once a night. Your data is
            not deleted, and the billing pages stay reachable so you can put it right.
          </Faq>
          <Faq q="Do candidates pay?">
            No. A candidate account, a profile, a résumé and applying to jobs are free.
            Plans on this page are for companies.
          </Faq>
          <Faq q="What does a limit of zero mean?">
            Exactly that: the feature is not part of that plan. A limit shown as
            unlimited has no cap at all. We do not use “unlimited” to mean “a lot”.
          </Faq>
          <Faq q="Can I change plan later?">
            Yes, from the billing section of your own portal once you are signed in.
          </Faq>
        </dl>

        <div className="mt-10 flex flex-wrap gap-3">
          <Button asChild variant="accent">
            <Link href="/signup/company">Create a company account</Link>
          </Button>
          <Button asChild variant="outline">
            <Link href="/for-companies">What you get</Link>
          </Button>
        </div>
      </Section>
    </>
  );
}

function Faq({ q, children }: { q: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="font-medium">{q}</dt>
      <dd className="mt-2 text-sm leading-relaxed text-muted-foreground">{children}</dd>
    </div>
  );
}
