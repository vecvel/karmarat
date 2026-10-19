import type { Metadata } from "next";
import { redirect } from "next/navigation";

import { readIdentity } from "@/lib/session";
import { LoginForm } from "./login-form";

export const metadata: Metadata = { title: "Sign in" };
export const dynamic = "force-dynamic";

/**
 * A server component guard, the same shape as every other route's.
 *
 * An operator who is already signed in has no business on this page; sending
 * them to the overview also means a bookmarked /login does not look broken.
 */
export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ reason?: string }>;
}) {
  if (await readIdentity()) {
    redirect("/");
  }

  const { reason } = await searchParams;

  return (
    <main className="flex min-h-dvh items-center justify-center bg-muted/40 px-4 py-12">
      <div className="w-full max-w-sm space-y-6">
        <div className="space-y-1.5 text-center">
          <p className="text-xs font-semibold uppercase tracking-[0.2em] text-copper-deep">
            Karmarat
          </p>
          <h1 className="text-2xl font-semibold tracking-tight">Platform console</h1>
          <p className="text-sm text-muted-foreground">
            For platform staff. Company and candidate accounts sign in on their own portals.
          </p>
        </div>

        <LoginForm expired={reason === "expired"} />
      </div>
    </main>
  );
}
