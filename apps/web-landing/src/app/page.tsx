import Link from "next/link";
import {
  Briefcase,
  KanbanSquare,
  MessagesSquare,
  Search,
  ShieldCheck,
  UserRoundCheck,
} from "lucide-react";

import { Container, Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { JOBS_PORTAL_URL, PORTAL_HOST } from "@/lib/env";

export default function HomePage() {
  return (
    <>
      <section className="border-b border-border bg-parchment-soft dark:bg-secondary">
        <Container className="py-16 sm:py-24">
          <div className="max-w-3xl">
            <h1 className="text-3xl leading-tight sm:text-5xl">
              A careers portal for your company. One job board for everyone else.
            </h1>
            <p className="mt-5 max-w-2xl text-base text-muted-foreground sm:text-lg">
              Karmarat is two things that belong together: recruitment software a
              company runs its hiring in, and a job board candidates search and apply
              through. A job posted once can appear on both.
            </p>
            <div className="mt-8 flex flex-wrap gap-3">
              <Button asChild variant="accent" size="lg">
                <Link href="/signup/company">Create a company account</Link>
              </Button>
              <Button asChild variant="outline" size="lg">
                <a href={JOBS_PORTAL_URL}>Find a job</a>
              </Button>
            </div>
            <p className="mt-4 text-sm text-muted-foreground">
              Companies get their own address, like{" "}
              <code className="rounded-xs bg-background px-1.5 py-0.5 tabular">
                acme.{PORTAL_HOST}
              </code>
              . Candidates get one account for every company on the platform.
            </p>
          </div>
        </Container>
      </section>

      <Section
        title="For companies"
        lead="Post a role, take applications through a pipeline you define, and keep the whole thing on an address that is yours."
      >
        <div className="grid gap-px overflow-hidden rounded-xs border border-border bg-border sm:grid-cols-2 lg:grid-cols-3">
          <Feature
            icon={<Briefcase aria-hidden="true" />}
            title="Requisitions and your own portal"
            body="Write a job once and choose where it appears: your careers portal, the Karmarat job network, or both. Each role can carry its own application form."
          />
          <Feature
            icon={<KanbanSquare aria-hidden="true" />}
            title="A pipeline you define"
            body="Stages are yours to name and reorder. Advance, reject with a reason, bulk-update, and export — the history stays attached to the application."
          />
          <Feature
            icon={<Search aria-hidden="true" />}
            title="Talent search"
            body="Search candidates who have chosen to be discoverable, and approach them directly. Candidates who switch discoverability off do not appear."
          />
          <Feature
            icon={<MessagesSquare aria-hidden="true" />}
            title="Messaging and notifications"
            body="Talk to candidates in the same place the application lives. In-app, real time, and by email when someone is away."
          />
          <Feature
            icon={<ShieldCheck aria-hidden="true" />}
            title="Roles that actually restrict"
            body="Sixty-five permissions, granted through a membership rather than pinned to a person. Revoking access at one employer leaves any other untouched."
          />
          <Feature
            icon={<UserRoundCheck aria-hidden="true" />}
            title="Separate tenants, enforced"
            body="Every request is resolved to a tenant at the gateway before a service sees it. A token issued for one company is refused on another company's address."
          />
        </div>

        <div className="mt-8 flex flex-wrap gap-3">
          <Button asChild variant="accent">
            <Link href="/signup/company">Create a company account</Link>
          </Button>
          <Button asChild variant="outline">
            <Link href="/pricing">See pricing</Link>
          </Button>
          <Button asChild variant="ghost">
            <Link href="/for-companies">What you get, in detail</Link>
          </Button>
        </div>
      </Section>

      <Section
        className="border-y border-border bg-muted/40"
        title="For candidates"
        lead="One account, one profile, one résumé — across every company hiring on Karmarat."
      >
        <div className="grid gap-6 sm:grid-cols-3">
          <Step
            number="1"
            title="Build a profile once"
            body="Your details and your résumé live with you, not with a company. You decide whether recruiters can find you in talent search, and you can change your mind."
          />
          <Step
            number="2"
            title="Find work and apply"
            body="Search jobs published to the network, or apply on a company's own careers page. Either way it is the same account, and you can only apply once per job."
          />
          <Step
            number="3"
            title="See where you stand"
            body="Track every application you have sent, read messages from recruiters, and get told when something moves."
          />
        </div>

        <div className="mt-8 flex flex-wrap gap-3">
          <Button asChild variant="accent">
            <a href={JOBS_PORTAL_URL}>Find a job</a>
          </Button>
          <Button asChild variant="outline">
            <a href={`${JOBS_PORTAL_URL}/register`}>Create a candidate account</a>
          </Button>
          <Button asChild variant="ghost">
            <Link href="/for-candidates">How it works</Link>
          </Button>
        </div>
      </Section>

      <Section>
        <div className="flex flex-col items-start justify-between gap-6 rounded-xs border border-border bg-card p-8 sm:flex-row sm:items-center">
          <div className="max-w-xl">
            <h2 className="text-xl">Ready to set up your company?</h2>
            <p className="mt-2 text-sm text-muted-foreground">
              Pick an address, create the owner account, and you are hiring. Your
              registration goes to the platform team for review before your portal opens.
            </p>
          </div>
          <Button asChild variant="accent" size="lg" className="shrink-0">
            <Link href="/signup/company">Get started</Link>
          </Button>
        </div>
      </Section>
    </>
  );
}

function Feature({
  icon,
  title,
  body,
}: {
  icon: React.ReactNode;
  title: string;
  body: string;
}) {
  return (
    <div className="bg-card p-6">
      <div className="text-accent [&_svg]:size-5">{icon}</div>
      <h3 className="mt-3 text-base">{title}</h3>
      <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{body}</p>
    </div>
  );
}

function Step({ number, title, body }: { number: string; title: string; body: string }) {
  return (
    <div>
      <span
        aria-hidden="true"
        className="inline-flex h-8 w-8 items-center justify-center rounded-full bg-primary text-sm font-semibold text-primary-foreground tabular"
      >
        {number}
      </span>
      <h3 className="mt-3 text-base">{title}</h3>
      <p className="mt-2 text-sm leading-relaxed text-muted-foreground">{body}</p>
    </div>
  );
}
