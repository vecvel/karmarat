"use client";

import { useId, useState } from "react";
import { Loader2 } from "lucide-react";
import type { ProblemError } from "@karmarat/ui";
import { useApi } from "@karmarat/ui/react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { formatInterval, formatMoney } from "@/lib/format";
import { SUBSCRIPTION_STATES, type Plan } from "@/lib/types";
import { asProblem } from "@/lib/use-resource";
import { cn } from "@/lib/utils";

/**
 * Subscribing a tenant from the console.
 *
 * The company id is typed rather than picked from a dropdown of every tenant:
 * this action reaches into one customer's billing, and an operator arrives here
 * from that customer's page with the id in hand. A list of all tenants in a
 * select makes the wrong one a mis-click away.
 */
export function CreateSubscriptionDialog({
  open,
  onOpenChange,
  plans,
  defaultCompanyId,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  plans: Plan[];
  defaultCompanyId: string;
  onCreated: () => void;
}) {
  const api = useApi();
  const companyId = useId();
  const planId = useId();
  const stateId = useId();
  const trialId = useId();

  const [company, setCompany] = useState(defaultCompanyId);
  const [plan, setPlan] = useState("");
  const [state, setState] = useState("");
  const [startTrial, setStartTrial] = useState(false);
  const [problem, setProblem] = useState<ProblemError | null>(null);
  const [saving, setSaving] = useState(false);

  const selectedPlan = plans.find((candidate) => candidate.id === plan);

  async function submit() {
    setSaving(true);
    setProblem(null);
    try {
      await api.post("/subscriptions", {
        companyId: company.trim(),
        planId: plan,
        ...(state ? { state } : {}),
        ...(startTrial ? { startTrial: true } : {}),
      });
      onCreated();
      onOpenChange(false);
      setPlan("");
      setState("");
      setStartTrial(false);
    } catch (caught) {
      setProblem(asProblem(caught));
    } finally {
      setSaving(false);
    }
  }

  const companyError = problem?.fieldError("companyId");
  const planError = problem?.fieldError("planId");
  const summary = problem && !companyError && !planError ? problem.detail : null;

  return (
    <Dialog open={open} onOpenChange={(next) => (saving ? undefined : onOpenChange(next))}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New subscription</DialogTitle>
          <DialogDescription>
            Subscribes a tenant directly, without a checkout. Use this for migrations and
            negotiated deals, not for self-service purchases.
          </DialogDescription>
        </DialogHeader>

        {summary ? (
          <p
            role="alert"
            className="rounded-xs border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {summary}
          </p>
        ) : null}

        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <div className="space-y-1.5">
            <Label htmlFor={companyId}>Company id</Label>
            <Input
              id={companyId}
              required
              value={company}
              onChange={(event) => setCompany(event.target.value)}
              placeholder="00000000-0000-0000-0000-000000000000"
              aria-invalid={companyError ? true : undefined}
              aria-describedby={companyError ? `${companyId}-error` : undefined}
              className={cn(companyError && "border-destructive")}
            />
            {companyError ? (
              <p id={`${companyId}-error`} className="text-xs text-destructive">
                {companyError}
              </p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor={planId}>Plan</Label>
            <select
              id={planId}
              required
              value={plan}
              onChange={(event) => setPlan(event.target.value)}
              aria-invalid={planError ? true : undefined}
              className={cn(
                "h-9 w-full rounded-xs border border-input bg-background px-3 text-sm",
                planError && "border-destructive",
              )}
            >
              <option value="">Choose a plan…</option>
              {plans.map((option) => (
                <option key={option.id} value={option.id}>
                  {option.name} — {formatMoney(option.price.amount, option.price.currency)}{" "}
                  {formatInterval(option.interval, option.intervalCount)}
                  {option.state !== "published" ? ` (${option.state})` : ""}
                </option>
              ))}
            </select>
            {planError ? (
              <p className="text-xs text-destructive">{planError}</p>
            ) : selectedPlan && selectedPlan.state !== "published" ? (
              <p className="text-xs text-warning">
                This plan is {selectedPlan.state}. It is not on the public pricing page.
              </p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor={stateId}>Initial state</Label>
            <select
              id={stateId}
              value={state}
              onChange={(event) => setState(event.target.value)}
              className="h-9 w-full rounded-xs border border-input bg-background px-3 text-sm"
            >
              <option value="">Let the service decide</option>
              {SUBSCRIPTION_STATES.map((value) => (
                <option key={value} value={value}>
                  {value.replace(/_/g, " ")}
                </option>
              ))}
            </select>
          </div>

          <div className="surface-muted flex items-center justify-between gap-3 px-3 py-2">
            <Label htmlFor={trialId} className="text-sm font-normal">
              Start the plan&rsquo;s trial period
              {selectedPlan ? ` (${selectedPlan.trialDays} days)` : ""}
            </Label>
            <Switch
              id={trialId}
              checked={startTrial}
              onCheckedChange={setStartTrial}
              disabled={selectedPlan ? selectedPlan.trialDays === 0 : false}
            />
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving || !plan || !company.trim()}>
              {saving ? <Loader2 className="animate-spin" aria-hidden /> : null}
              Create subscription
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
