"use client";

import { useId, useState, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { ProblemError } from "@karmarat/ui";
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
import { Textarea } from "@/components/ui/textarea";
import { minorToInput, parseMoneyToMinor } from "@/lib/format";
import {
  PLAN_INTERVALS,
  SUPPORT_TIERS,
  type Entitlements,
  type Plan,
  type SupportTier,
} from "@/lib/types";
import { asProblem } from "@/lib/use-resource";
import { cn } from "@/lib/utils";

/** An unset numeric limit is "", unlimited is null, none is 0 — three states. */
type LimitInput = { unlimited: boolean; value: string };

interface FormState {
  key: string;
  name: string;
  description: string;
  price: string;
  currency: string;
  interval: string;
  intervalCount: string;
  trialDays: string;
  sortOrder: string;
  maxJobs: LimitInput;
  maxRecruiters: LimitInput;
  maxApplicationsPerMonth: LimitInput;
  canPublishToNetwork: boolean;
  canUseTalentSearch: boolean;
  canUseMessaging: boolean;
  supportTier: SupportTier;
  storageGb: string;
}

function limitFrom(value: number | null): LimitInput {
  return value === null ? { unlimited: true, value: "" } : { unlimited: false, value: String(value) };
}

function blankForm(): FormState {
  return {
    key: "",
    name: "",
    description: "",
    price: "0.00",
    currency: "USD",
    interval: "month",
    intervalCount: "1",
    trialDays: "0",
    sortOrder: "0",
    // Every entitlement starts explicit rather than empty: the API rejects a
    // partial map so a missing limit cannot quietly default to permissive, and
    // the form should not be able to produce one either.
    maxJobs: { unlimited: false, value: "0" },
    maxRecruiters: { unlimited: false, value: "0" },
    maxApplicationsPerMonth: { unlimited: false, value: "0" },
    canPublishToNetwork: false,
    canUseTalentSearch: false,
    canUseMessaging: false,
    supportTier: "community",
    storageGb: "0",
  };
}

function formFrom(plan: Plan): FormState {
  return {
    key: plan.key,
    name: plan.name,
    description: plan.description,
    price: minorToInput(plan.price.amount, plan.price.currency),
    currency: plan.price.currency,
    interval: plan.interval,
    intervalCount: String(plan.intervalCount),
    trialDays: String(plan.trialDays),
    sortOrder: String(plan.sortOrder),
    maxJobs: limitFrom(plan.entitlements.maxJobs),
    maxRecruiters: limitFrom(plan.entitlements.maxRecruiters),
    maxApplicationsPerMonth: limitFrom(plan.entitlements.maxApplicationsPerMonth),
    canPublishToNetwork: plan.entitlements.canPublishToNetwork,
    canUseTalentSearch: plan.entitlements.canUseTalentSearch,
    canUseMessaging: plan.entitlements.canUseMessaging,
    supportTier: plan.entitlements.supportTier,
    storageGb: String(plan.entitlements.storageGb),
  };
}

function limitValue(input: LimitInput): number | null {
  if (input.unlimited) return null;
  const parsed = Number(input.value.trim());
  return Number.isInteger(parsed) && parsed >= 0 ? parsed : 0;
}

export function PlanEditor({
  open,
  onOpenChange,
  plan,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** null creates; a plan edits it in place. */
  plan: Plan | null;
  onSaved: () => void;
}) {
  const api = useApi();
  const [form, setForm] = useState<FormState>(() => (plan ? formFrom(plan) : blankForm()));
  const [problem, setProblem] = useState<ProblemError | null>(null);
  const [saving, setSaving] = useState(false);

  // Remount-free reset: the dialog is kept mounted so the trigger keeps focus,
  // so the form is reset when it opens rather than when it unmounts.
  const [lastPlanId, setLastPlanId] = useState<string | null>(plan?.id ?? null);
  if (open && (plan?.id ?? null) !== lastPlanId) {
    setLastPlanId(plan?.id ?? null);
    setForm(plan ? formFrom(plan) : blankForm());
    setProblem(null);
  }

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) =>
    setForm((current) => ({ ...current, [key]: value }));

  async function save() {
    setSaving(true);
    setProblem(null);

    const priceAmount = parseMoneyToMinor(form.price, form.currency);
    if (priceAmount === null) {
      // Shaped like the server's own 422 so the field message renders in the
      // same place whether the browser caught it or the API did.
      setProblem(
        new ProblemError({
          type: "about:blank",
          title: "Unprocessable Entity",
          status: 422,
          detail: "Check the highlighted fields and try again.",
          code: "validation_failed",
          errors: { priceAmount: ["Enter a price as a non-negative number, for example 49.00."] },
        }),
      );
      setSaving(false);
      return;
    }

    const entitlements: Entitlements = {
      maxJobs: limitValue(form.maxJobs),
      maxRecruiters: limitValue(form.maxRecruiters),
      maxApplicationsPerMonth: limitValue(form.maxApplicationsPerMonth),
      canPublishToNetwork: form.canPublishToNetwork,
      canUseTalentSearch: form.canUseTalentSearch,
      canUseMessaging: form.canUseMessaging,
      supportTier: form.supportTier,
      storageGb: Number(form.storageGb.trim()) || 0,
    };

    const body = {
      key: form.key.trim(),
      name: form.name.trim(),
      description: form.description.trim(),
      priceAmount,
      currency: form.currency.trim().toUpperCase(),
      interval: form.interval,
      // A lifetime plan has no interval count to speak of; sending one is how a
      // "forever" plan ends up with an expiry date.
      ...(form.interval === "lifetime" ? {} : { intervalCount: Number(form.intervalCount) || 1 }),
      trialDays: Number(form.trialDays) || 0,
      sortOrder: Number(form.sortOrder) || 0,
      entitlements,
    };

    try {
      if (plan) {
        await api.patch(`/plans/${plan.id}`, body);
      } else {
        await api.post("/plans", body);
      }
      onSaved();
      onOpenChange(false);
    } catch (caught) {
      setProblem(asProblem(caught));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => (saving ? undefined : onOpenChange(next))}>
      <DialogContent className="max-h-[90dvh] max-w-3xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{plan ? `Edit ${plan.name}` : "New plan"}</DialogTitle>
          <DialogDescription>
            Prices are entered in major units and stored in minor units. Every entitlement must be
            set — the API refuses a partial map so an unset limit can never read as unlimited.
          </DialogDescription>
        </DialogHeader>

        {problem ? (
          <p
            role="alert"
            className="rounded-xs border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {problem.detail}
          </p>
        ) : null}

        <form
          className="space-y-6"
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <fieldset className="space-y-4">
            <legend className="section-title">Catalogue</legend>
            <div className="grid gap-4 sm:grid-cols-2">
              <TextField
                label="Key"
                value={form.key}
                onChange={(value) => set("key", value)}
                problem={problem}
                field="key"
                hint="3–50 lowercase letters, digits, hyphens or underscores. Appears in URLs."
                required
              />
              <TextField
                label="Name"
                value={form.name}
                onChange={(value) => set("name", value)}
                problem={problem}
                field="name"
                required
              />
            </div>
            <TextAreaField
              label="Description"
              value={form.description}
              onChange={(value) => set("description", value)}
              problem={problem}
              field="description"
            />
          </fieldset>

          <fieldset className="space-y-4">
            <legend className="section-title">Price</legend>
            <div className="grid gap-4 sm:grid-cols-4">
              <TextField
                label="Amount"
                value={form.price}
                onChange={(value) => set("price", value)}
                problem={problem}
                field="priceAmount"
                inputMode="decimal"
                hint={`Stored as ${parseMoneyToMinor(form.price, form.currency) ?? 0} minor units`}
                required
              />
              <TextField
                label="Currency"
                value={form.currency}
                onChange={(value) => set("currency", value.toUpperCase())}
                problem={problem}
                field="currency"
                maxLength={3}
                required
              />
              <SelectField
                label="Interval"
                value={form.interval}
                onChange={(value) => set("interval", value)}
                options={PLAN_INTERVALS.map((value) => ({ value, label: value }))}
                problem={problem}
                field="interval"
              />
              {form.interval === "lifetime" ? (
                <div className="space-y-1.5">
                  <span className="field-label">Interval count</span>
                  <p className="pt-2 text-sm text-muted-foreground">
                    Not applicable — a lifetime plan never renews.
                  </p>
                </div>
              ) : (
                <TextField
                  label="Interval count"
                  value={form.intervalCount}
                  onChange={(value) => set("intervalCount", value)}
                  problem={problem}
                  field="intervalCount"
                  inputMode="numeric"
                />
              )}
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <TextField
                label="Trial days"
                value={form.trialDays}
                onChange={(value) => set("trialDays", value)}
                problem={problem}
                field="trialDays"
                inputMode="numeric"
              />
              <TextField
                label="Sort order"
                value={form.sortOrder}
                onChange={(value) => set("sortOrder", value)}
                problem={problem}
                field="sortOrder"
                inputMode="numeric"
                hint="Lower sorts first on the pricing page."
              />
            </div>
          </fieldset>

          <fieldset className="space-y-4">
            <legend className="section-title">Entitlements</legend>
            <p className="text-xs text-muted-foreground">
              A limit of <strong>0</strong> means the tenant may do none of it. Ticking{" "}
              <strong>Unlimited</strong> sends <code>null</code>, which means no ceiling at all.
              They are different answers and the API stores them differently.
            </p>

            <div className="grid gap-4 sm:grid-cols-3">
              <LimitField
                label="Max jobs"
                value={form.maxJobs}
                onChange={(value) => set("maxJobs", value)}
                problem={problem}
                field="entitlements.maxJobs"
              />
              <LimitField
                label="Max recruiters"
                value={form.maxRecruiters}
                onChange={(value) => set("maxRecruiters", value)}
                problem={problem}
                field="entitlements.maxRecruiters"
              />
              <LimitField
                label="Max applications / month"
                value={form.maxApplicationsPerMonth}
                onChange={(value) => set("maxApplicationsPerMonth", value)}
                problem={problem}
                field="entitlements.maxApplicationsPerMonth"
              />
            </div>

            <div className="grid gap-3 sm:grid-cols-3">
              <ToggleField
                label="Publish to network"
                checked={form.canPublishToNetwork}
                onChange={(value) => set("canPublishToNetwork", value)}
              />
              <ToggleField
                label="Talent search"
                checked={form.canUseTalentSearch}
                onChange={(value) => set("canUseTalentSearch", value)}
              />
              <ToggleField
                label="Messaging"
                checked={form.canUseMessaging}
                onChange={(value) => set("canUseMessaging", value)}
              />
            </div>

            <div className="grid gap-4 sm:grid-cols-2">
              <SelectField
                label="Support tier"
                value={form.supportTier}
                onChange={(value) => set("supportTier", value as SupportTier)}
                options={SUPPORT_TIERS.map((value) => ({ value, label: value }))}
                problem={problem}
                field="entitlements.supportTier"
              />
              <TextField
                label="Storage (GB)"
                value={form.storageGb}
                onChange={(value) => set("storageGb", value)}
                problem={problem}
                field="entitlements.storageGb"
                inputMode="numeric"
              />
            </div>
          </fieldset>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={saving}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? <Loader2 className="animate-spin" aria-hidden /> : null}
              {plan ? "Save changes" : "Create plan"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/* --------------------------------------------------------------- fields -- */

function FieldShell({
  label,
  id,
  error,
  hint,
  children,
}: {
  label: string;
  id: string;
  error?: string | undefined;
  hint?: string | undefined;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error ? (
        <p id={`${id}-error`} className="text-xs text-destructive">
          {error}
        </p>
      ) : hint ? (
        <p id={`${id}-hint`} className="text-xs text-muted-foreground">
          {hint}
        </p>
      ) : null}
    </div>
  );
}

function TextField({
  label,
  value,
  onChange,
  problem,
  field,
  hint,
  required,
  inputMode,
  maxLength,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  problem: ProblemError | null;
  field: string;
  hint?: string;
  required?: boolean;
  inputMode?: "numeric" | "decimal";
  maxLength?: number;
}) {
  const id = useId();
  const error = problem?.fieldError(field);

  return (
    <FieldShell label={label} id={id} error={error} hint={hint}>
      <Input
        id={id}
        value={value}
        required={required}
        inputMode={inputMode}
        maxLength={maxLength}
        onChange={(event) => onChange(event.target.value)}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : hint ? `${id}-hint` : undefined}
        className={cn(error && "border-destructive")}
      />
    </FieldShell>
  );
}

function TextAreaField({
  label,
  value,
  onChange,
  problem,
  field,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  problem: ProblemError | null;
  field: string;
}) {
  const id = useId();
  const error = problem?.fieldError(field);

  return (
    <FieldShell label={label} id={id} error={error}>
      <Textarea
        id={id}
        rows={3}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : undefined}
        className={cn(error && "border-destructive")}
      />
    </FieldShell>
  );
}

function SelectField({
  label,
  value,
  onChange,
  options,
  problem,
  field,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  problem: ProblemError | null;
  field: string;
}) {
  const id = useId();
  const error = problem?.fieldError(field);

  return (
    <FieldShell label={label} id={id} error={error}>
      <select
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        aria-invalid={error ? true : undefined}
        className={cn(
          "h-9 w-full rounded-xs border border-input bg-background px-3 text-sm",
          error && "border-destructive",
        )}
      >
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label.charAt(0).toUpperCase() + option.label.slice(1)}
          </option>
        ))}
      </select>
    </FieldShell>
  );
}

/** A numeric limit with its own "unlimited" switch, because null ≠ 0. */
function LimitField({
  label,
  value,
  onChange,
  problem,
  field,
}: {
  label: string;
  value: LimitInput;
  onChange: (value: LimitInput) => void;
  problem: ProblemError | null;
  field: string;
}) {
  const id = useId();
  const switchId = `${id}-unlimited`;
  const error = problem?.fieldError(field);

  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        value={value.unlimited ? "" : value.value}
        inputMode="numeric"
        disabled={value.unlimited}
        placeholder={value.unlimited ? "Unlimited" : "0"}
        onChange={(event) => onChange({ ...value, value: event.target.value })}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : undefined}
        className={cn(error && "border-destructive")}
      />
      <div className="flex items-center gap-2">
        <Switch
          id={switchId}
          checked={value.unlimited}
          onCheckedChange={(checked) => onChange({ unlimited: checked, value: value.value })}
        />
        <Label htmlFor={switchId} className="text-xs font-normal text-muted-foreground">
          Unlimited
        </Label>
      </div>
      {error ? (
        <p id={`${id}-error`} className="text-xs text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
}

function ToggleField({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  const id = useId();
  return (
    <div className="surface-muted flex items-center justify-between gap-3 px-3 py-2">
      <Label htmlFor={id} className="text-sm font-normal">
        {label}
      </Label>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </div>
  );
}
