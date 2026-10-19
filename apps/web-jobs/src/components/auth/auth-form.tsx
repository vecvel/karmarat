"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";
import { ProblemError, type Session } from "@karmarat/ui";

import { FieldError, ProblemAlert } from "@/components/feedback";
import { useSession } from "@/components/session-provider";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

/**
 * Sign-in and registration, which are the same form with one extra field.
 *
 * Both post to a route handler on this origin rather than to the gateway. The
 * handler calls the gateway with this portal's hostname — so the realm follows
 * from where the request arrived, not from a field a client sets — and keeps
 * the refresh token in an httpOnly cookie the browser never sees.
 */
export function AuthForm({ mode }: { mode: "sign-in" | "register" }) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { adopt } = useSession();

  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [failure, setFailure] = useState<unknown>(null);
  const [submitting, setSubmitting] = useState(false);

  const registering = mode === "register";
  const nextPath = safeNext(searchParams.get("next"));

  const fieldError = (name: string) =>
    failure instanceof ProblemError ? failure.fieldError(name) : undefined;

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setFailure(null);
    setSubmitting(true);

    try {
      const response = await fetch(
        registering ? "/api/auth/register" : "/api/auth/login",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(
            registering ? { fullName, email, password } : { email, password },
          ),
        },
      );

      if (!response.ok) {
        setFailure(await ProblemError.fromResponse(response));
        return;
      }

      adopt((await response.json()) as Session);
      router.replace(nextPath);
      // The shell is server-rendered from the identity cookie, so it has to be
      // re-fetched for the signed-in header to appear.
      router.refresh();
    } catch {
      setFailure(
        new ProblemError({
          type: "about:blank",
          title: "Network Error",
          status: 0,
          detail:
            "We could not reach Karmarat. Check your connection and try again.",
          code: "network_error",
        }),
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form onSubmit={submit} noValidate className="space-y-4">
      {failure ? <ProblemAlert error={failure} /> : null}

      {registering ? (
        <div className="space-y-1.5">
          <Label htmlFor="fullName">Full name</Label>
          <Input
            id="fullName"
            name="fullName"
            autoComplete="name"
            required
            value={fullName}
            aria-invalid={fieldError("fullName") ? true : undefined}
            aria-describedby={fieldError("fullName") ? "fullName-error" : undefined}
            onChange={(event) => setFullName(event.target.value)}
          />
          <p id="fullName-error">
            <FieldError message={fieldError("fullName")} />
          </p>
        </div>
      ) : null}

      <div className="space-y-1.5">
        <Label htmlFor="email">Email</Label>
        <Input
          id="email"
          name="email"
          type="email"
          autoComplete="email"
          required
          value={email}
          aria-invalid={fieldError("email") ? true : undefined}
          aria-describedby={fieldError("email") ? "email-error" : undefined}
          onChange={(event) => setEmail(event.target.value)}
        />
        <p id="email-error">
          <FieldError message={fieldError("email")} />
        </p>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="password">Password</Label>
        <Input
          id="password"
          name="password"
          type="password"
          autoComplete={registering ? "new-password" : "current-password"}
          required
          value={password}
          aria-invalid={fieldError("password") ? true : undefined}
          aria-describedby={
            registering ? "password-help password-error" : "password-error"
          }
          onChange={(event) => setPassword(event.target.value)}
        />
        {registering ? (
          <p id="password-help" className="text-xs text-muted-foreground">
            The stronger the better — the service sets the minimum and will say
            so if yours falls short.
          </p>
        ) : null}
        <p id="password-error">
          <FieldError message={fieldError("password")} />
        </p>
      </div>

      <Button type="submit" variant="accent" className="w-full" disabled={submitting}>
        {submitting
          ? registering
            ? "Creating your account…"
            : "Signing in…"
          : registering
            ? "Create account"
            : "Sign in"}
      </Button>

      <p className="text-center text-sm text-muted-foreground">
        {registering ? (
          <>
            Already have an account?{" "}
            <Link
              href={`/sign-in${nextPath === "/" ? "" : `?next=${encodeURIComponent(nextPath)}`}`}
              className="text-accent underline underline-offset-2"
            >
              Sign in
            </Link>
          </>
        ) : (
          <>
            New here?{" "}
            <Link
              href={`/register${nextPath === "/" ? "" : `?next=${encodeURIComponent(nextPath)}`}`}
              className="text-accent underline underline-offset-2"
            >
              Create an account
            </Link>
          </>
        )}
      </p>
    </form>
  );
}

/**
 * Only same-site paths are followed after signing in.
 *
 * `?next=https://elsewhere.example` in a link would otherwise turn this form
 * into an open redirect — the kind a phishing page uses to borrow a real
 * domain's credibility on its way somewhere else.
 */
function safeNext(value: string | null): string {
  if (!value) return "/";
  if (!value.startsWith("/") || value.startsWith("//")) return "/";
  return value;
}
