import type { Metadata } from "next";
import { Suspense } from "react";

import { LoginForm } from "@/features/auth/components/login-form";

export const metadata: Metadata = { title: "Sign in - Karmarat" };

export default function LoginPage() {
  return (
    <div className="flex flex-col gap-6">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">Sign in</h1>
        <p className="mt-1.5 text-sm text-muted-foreground">My Organisation</p>
      </header>

      <Suspense fallback={<div className="text-sm text-muted-foreground">Loading form...</div>}>
        <LoginForm />
      </Suspense>
    </div>
  );
}
