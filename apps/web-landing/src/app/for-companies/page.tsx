import type { Metadata } from "next";
import Link from "next/link";

import { Container, Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { PORTAL_HOST } from "@/lib/env";

export const metadata: Metadata = {
  title: "For companies",
  description:
    "What a Karmarat company account includes: a careers portal, a hiring pipeline, talent search, messaging and role-based access.",
};

export default function ForCompaniesPage() {
  return (
    <>
      <section className="border-b border-border bg-parchment-soft dark:bg-secondary">
        <Container className="py-12 sm:py-16">
          <h1 className="text-3xl sm:text-4xl">For companies</h1>
          <p className="mt-3 max-w-2xl text-muted-foreground">
            Everything below is built and running. Where something is not finished, this
            page says so rather than describing it in the present tense.
          </p>
        </Container>
      </section>

      <Section>
        <div className="grid gap-x-12 gap-y-10 md:grid-cols-2">
          <Block title="Your own careers portal">
            Your company gets an address of its own —{" "}
            <span className="tabular">acme.{PORTAL_HOST}</span> — with your name, your
            colours and your open roles on it. It is a separate portal, not a page inside
            ours, and the gateway treats it as a separate tenant on every request.
          </Block>

          <Block title="Jobs, published where you choose">
            A requisition can go to your careers portal, to the Karmarat job network,
            to both, or to neither while you draft it. Each job can carry its own
            application form, so you ask a warehouse candidate different questions from a
            staff engineer.
          </Block>

          <Block title="A pipeline that matches how you hire">
            Stages are yours to define, name and reorder. Applications advance through
            them, can be rejected with a reason, bulk-updated and exported. One candidate
            can only hold one application per job, enforced in the database rather than
            hoped for in the UI.
          </Block>

          <Block title="Talent search, with consent">
            Search candidate profiles and approach people directly. Only candidates who
            have switched discoverability on are searchable, and switching it off removes
            them — this is a candidate-controlled setting, not a company one.
          </Block>

          <Block title="Messaging and notifications">
            Recruiters and candidates talk in the product, next to the application. In-app
            notifications arrive in real time, and email picks up whoever is not looking.
          </Block>

          <Block title="Roles that genuinely restrict">
            Sixty-five company permissions — who may publish to the network, who may see
            compensation on an offer, who may download a résumé. Permissions are held
            through a membership, so one person can work for two companies on the platform
            without either seeing the other, and you may only grant permissions you hold
            yourself.
          </Block>

          <Block title="Billing and support in the product">
            Plans, invoices and payment live in your portal. So does a support desk that
            reaches the platform team, with their internal notes hidden at the query
            level rather than filtered in the browser.
          </Block>

          <Block title="Your data stays yours">
            Résumés and company assets are in object storage under a key prefixed with
            your tenant id, and downloads are short-lived signed links issued only after
            the same permission checks a read would get.
          </Block>
        </div>

        <div className="mt-12 flex flex-wrap gap-3">
          <Button asChild variant="accent" size="lg">
            <Link href="/signup/company">Create a company account</Link>
          </Button>
          <Button asChild variant="outline" size="lg">
            <Link href="/pricing">See pricing</Link>
          </Button>
        </div>
      </Section>
    </>
  );
}

function Block({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h2 className="text-lg">{title}</h2>
      <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{children}</p>
    </div>
  );
}
