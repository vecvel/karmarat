import type { Metadata } from "next";

import { Container, Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { JOBS_PORTAL_URL } from "@/lib/env";

export const metadata: Metadata = {
  title: "For candidates",
  description:
    "One free Karmarat account: your profile and résumé, every job on the network, and every application you have sent in one place.",
};

/**
 * Registration lives on the jobs portal, not here.
 *
 * A candidate account belongs to the candidate realm, which the gateway
 * resolves from `jobs.{hostname}`. A sign-up form on this hostname would have
 * to tell the server which realm it meant, and a realm the client asserts is
 * not a realm. So this page explains and links.
 */
export default function ForCandidatesPage() {
  return (
    <>
      <section className="border-b border-border bg-parchment-soft dark:bg-secondary">
        <Container className="py-12 sm:py-16">
          <h1 className="text-3xl sm:text-4xl">For candidates</h1>
          <p className="mt-3 max-w-2xl text-muted-foreground">
            Free, and free for good. Companies pay for Karmarat; you do not, and your
            profile is not sold to them.
          </p>
          <div className="mt-8 flex flex-wrap gap-3">
            <Button asChild variant="accent" size="lg">
              <a href={`${JOBS_PORTAL_URL}/register`}>Create a candidate account</a>
            </Button>
            <Button asChild variant="outline" size="lg">
              <a href={JOBS_PORTAL_URL}>Browse jobs first</a>
            </Button>
          </div>
        </Container>
      </section>

      <Section title="What you get">
        <div className="grid gap-x-12 gap-y-10 md:grid-cols-2">
          <Block title="One profile, every company">
            Your details and your résumé belong to your account, not to a company&rsquo;s
            database. Apply to a job on the network or on a company&rsquo;s own careers
            page — it is the same account either way, and you do not retype anything.
          </Block>

          <Block title="You control whether you can be found">
            Recruiters can search candidate profiles, but only those who have turned
            discoverability on. It is off until you say otherwise, and turning it off
            again removes you from their results.
          </Block>

          <Block title="Apply once, then watch it move">
            One application per job — no accidental duplicates. You can see which stage
            each application has reached, and you are told when it changes.
          </Block>

          <Block title="Talk to the recruiter">
            Messages from a company arrive in your account, attached to the job they are
            about, and you can reply there. Nothing goes through a personal inbox unless
            you want the email notification.
          </Block>

          <Block title="A separate identity from your work account">
            If you also recruit for a company on Karmarat, the two accounts can share
            an email address and stay completely separate. Leaving that employer does not
            touch your candidate account.
          </Block>

          <Block title="Leaving is a button">
            You can delete your candidate profile. Applications you have already sent are
            a record the company holds, and the platform will not pretend otherwise.
          </Block>
        </div>

        <div className="mt-12 flex flex-wrap gap-3">
          <Button asChild variant="accent" size="lg">
            <a href={`${JOBS_PORTAL_URL}/register`}>Create a candidate account</a>
          </Button>
          <Button asChild variant="outline" size="lg">
            <a href={`${JOBS_PORTAL_URL}/login`}>Sign in</a>
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
