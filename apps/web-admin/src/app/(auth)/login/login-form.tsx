"use client";

import { useRouter } from "next/navigation";
import { useId, useState, type FormEvent } from "react";
import { Loader2 } from "lucide-react";
import { ProblemError } from "@karmarat/ui";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/**
 * Sign-in.
 *
 * Posts to this app's own route handler, which adds `realm: "platform"` and
 * calls the gateway on the portal's hostname. The realm is not a field here on
 * purpose: which realm a sign-in targets is decided by which portal received
 * it, not by anything the browser can be persuaded to send.
 */
export function LoginForm({ expired }: { expired: boolean }) {
  const router = useRouter();
  const emailId = useId();
  const passwordId = useId();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [problem, setProblem] = useState<ProblemError | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitting(true);
    setProblem(null);

    try {
      const response = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "content-type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ email, password }),
      });

      if (!response.ok) {
        setProblem(await ProblemError.fromResponse(response));
        return;
      }

      // refresh() before push() so the console layout's server-side guard sees
      // the cookies that were just set rather than a cached signed-out render.
      router.refresh();
      router.push("/");
    } catch {
      setProblem(
        new ProblemError({
          type: "about:blank",
          title: "Network Error",
          status: 0,
          detail: "We could not reach the console. Check your connection and try again.",
          code: "network_error",
        }),
      );
    } finally {
      setSubmitting(false);
    }
  }

  const emailError = problem?.fieldError("email");
  const passwordError = problem?.fieldError("password");
  // A field-level message is rendered beside its input; anything else is a
  // summary and belongs at the top of the form.
  const summary = problem && !emailError && !passwordError ? problem.detail : null;

  return (
    <form onSubmit={onSubmit} className="surface space-y-4 p-6" noValidate>
      {expired && !problem ? (
        <p role="status" className="surface-muted px-3 py-2 text-sm text-muted-foreground">
          Your session ended. Sign in again to continue.
        </p>
      ) : null}

      {summary ? (
        <p
          role="alert"
          className="rounded-xs border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          {summary}
        </p>
      ) : null}

      <div className="space-y-1.5">
        <Label htmlFor={emailId}>Work email</Label>
        <Input
          id={emailId}
          name="email"
          type="email"
          autoComplete="username"
          required
          autoFocus
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          aria-invalid={emailError ? true : undefined}
          aria-describedby={emailError ? `${emailId}-error` : undefined}
          className={cn(emailError && "border-destructive")}
        />
        {emailError ? (
          <p id={`${emailId}-error`} className="text-xs text-destructive">
            {emailError}
          </p>
        ) : null}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor={passwordId}>Password</Label>
        <Input
          id={passwordId}
          name="password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          aria-invalid={passwordError ? true : undefined}
          aria-describedby={passwordError ? `${passwordId}-error` : undefined}
          className={cn(passwordError && "border-destructive")}
        />
        {passwordError ? (
          <p id={`${passwordId}-error`} className="text-xs text-destructive">
            {passwordError}
          </p>
        ) : null}
      </div>

      <Button type="submit" className="w-full" disabled={submitting}>
        {submitting ? <Loader2 className="animate-spin" aria-hidden /> : null}
        {submitting ? "Signing in…" : "Sign in"}
      </Button>
    </form>
  );
}
