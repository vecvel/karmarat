import type { Metadata } from "next";
import { redirect } from "next/navigation";
import { ArrowRight } from "lucide-react";

import { Container } from "@/components/section";
import { Field } from "@/components/field";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  JOBS_PORTAL_URL,
  PORTAL_HOST,
  ROOT_PORTAL_URL,
  companyPortalUrl,
} from "@/lib/env";
import { normaliseSlug, slugProblem } from "@/lib/slug";

export const metadata: Metadata = {
  title: "Sign in",
  description: "Find the right Karmarat portal to sign in at.",
};

/**
 * There is no sign-in form on this site, and there must not be one.
 *
 * The gateway decides the portal and the realm from the hostname a request
 * arrives on. A single form here could not say which of three realms a person
 * belongs to without the client asserting it — which is exactly the design the
 * platform rejects. So this page routes people to the right door instead.
 */

/** Why an address could not be used. Codes, not free text, so a crafted URL
 *  cannot put words on the page. */
const ERRORS: Record<string, string> = {
  invalid: "That does not look like a company address. Check it and try again.",
};

async function goToCompanyPortal(formData: FormData): Promise<void> {
  "use server";

  const slug = normaliseSlug(String(formData.get("slug") ?? ""));
  if (slugProblem(slug) !== null) {
    redirect("/signin?error=invalid");
  }
  redirect(companyPortalUrl(slug));
}

export default async function SignInPage({
  searchParams,
}: {
  searchParams: Promise<{ error?: string }>;
}) {
  const params = await searchParams;
  const error = params.error ? ERRORS[params.error] : undefined;

  return (
    <Container className="max-w-3xl py-12 sm:py-16">
      <h1 className="text-3xl">Sign in</h1>
      <p className="mt-3 text-muted-foreground">
        Karmarat has a separate portal for each kind of account, so there is no
        single sign-in page. Pick the one that describes you.
      </p>

      <div className="mt-10 flex flex-col gap-4">
        <Door
          title="I am looking for a job"
          body="Candidate accounts live on the jobs portal — one account for every company hiring on Karmarat."
          href={`${JOBS_PORTAL_URL}/login`}
          cta="Go to the jobs portal"
        />

        <section className="rounded-xs border border-border bg-card p-6">
          <h2 className="text-base">I work for a company that hires here</h2>
          <p className="mt-2 text-sm text-muted-foreground">
            You sign in at your company&rsquo;s own address, not here. Enter it and we
            will send you there.
          </p>

          {/* A plain server action: no client JavaScript, and it still works
              with the keyboard, with autofill and with JS switched off. */}
          <form action={goToCompanyPortal} className="mt-4 flex flex-col gap-3 sm:max-w-md">
            <Field id="company-slug" label="Company address" error={error}>
              {(props) => (
                <div className="flex items-stretch">
                  <Input
                    {...props}
                    name="slug"
                    placeholder="acme"
                    autoCapitalize="none"
                    autoCorrect="off"
                    spellCheck={false}
                    className="rounded-r-none"
                  />
                  <span
                    aria-hidden="true"
                    className="inline-flex shrink-0 items-center border border-l-0 border-input bg-muted px-3 text-sm text-muted-foreground"
                  >
                    .{PORTAL_HOST}
                  </span>
                </div>
              )}
            </Field>
            <Button type="submit" variant="outline" className="self-start">
              Go to my company
              <ArrowRight aria-hidden="true" />
            </Button>
          </form>
        </section>

        <Door
          title="I am platform staff"
          body="The platform console is on the root portal."
          href={ROOT_PORTAL_URL}
          cta="Go to the platform console"
        />
      </div>

      <p className="mt-10 text-sm text-muted-foreground">
        No company account yet?{" "}
        <a href="/signup/company" className="underline underline-offset-4">
          Create one
        </a>
        .
      </p>
    </Container>
  );
}

function Door({
  title,
  body,
  href,
  cta,
}: {
  title: string;
  body: string;
  href: string;
  cta: string;
}) {
  return (
    <section className="flex flex-col items-start justify-between gap-4 rounded-xs border border-border bg-card p-6 sm:flex-row sm:items-center">
      <div>
        <h2 className="text-base">{title}</h2>
        <p className="mt-2 text-sm text-muted-foreground">{body}</p>
      </div>
      <Button asChild variant="outline" className="shrink-0">
        <a href={href}>
          {cta}
          <ArrowRight aria-hidden="true" />
        </a>
      </Button>
    </section>
  );
}
