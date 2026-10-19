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
import { Textarea } from "@/components/ui/textarea";
import { SUBSCRIPTION_STATES, type Entitlements, type Subscription } from "@/lib/types";
import { asProblem } from "@/lib/use-resource";
import { cn } from "@/lib/utils";

const MIN_REASON = 3;

/**
 * Overriding a subscription.
 *
 * This is the one action in billing that lets an operator hand a tenant
 * entitlements they did not buy, so the service audits it and requires a
 * reason. The form makes the reason mandatory before the button is usable
 * rather than letting the request fail — an audit trail full of round-trips is
 * a form that treated the requirement as the server's problem.
 *
 * Entitlements are only sent when the operator deliberately opens that section,
 * because sending them is what pins the tenant's limits to this snapshot; a
 * state-only override must not silently freeze their plan's future changes.
 */
export function OverrideDialog({
  subscription,
  onClose,
  onSaved,
}: {
  subscription: Subscription | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const api = useApi();
  const stateId = useId();
  const expiresId = useId();
  const reasonId = useId();
  const entitlementsId = useId();

  const [state, setState] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [reason, setReason] = useState("");
  const [editEntitlements, setEditEntitlements] = useState(false);
  const [entitlements, setEntitlements] = useState<Entitlements | null>(null);
  const [problem, setProblem] = useState<ProblemError | null>(null);
  const [saving, setSaving] = useState(false);

  // The dialog stays mounted; reset when a different subscription opens it.
  const [lastId, setLastId] = useState<string | null>(null);
  if (subscription && subscription.id !== lastId) {
    setLastId(subscription.id);
    setState("");
    setExpiresAt("");
    setReason("");
    setEditEntitlements(false);
    setEntitlements(subscription.entitlements);
    setProblem(null);
  }

  const reasonError = problem?.fieldError("reason");
  const tooShort = reason.trim().length < MIN_REASON;

  async function submit() {
    if (!subscription) return;
    setSaving(true);
    setProblem(null);

    try {
      await api.post(`/subscriptions/${subscription.id}/override`, {
        reason: reason.trim(),
        ...(state ? { state } : {}),
        // An empty date field means "leave it"; clearing an expiry is done by
        // typing a date, not by submitting nothing.
        ...(expiresAt ? { expiresAt: new Date(expiresAt).toISOString() } : {}),
        ...(editEntitlements && entitlements ? { entitlements } : {}),
      });
      onSaved();
      onClose();
    } catch (caught) {
      setProblem(asProblem(caught));
    } finally {
      setSaving(false);
    }
  }

  const setLimit = (key: keyof Entitlements, value: Entitlements[keyof Entitlements]) =>
    setEntitlements((current) => (current ? { ...current, [key]: value } : current));

  return (
    <Dialog open={subscription !== null} onOpenChange={(open) => (!open && !saving ? onClose() : undefined)}>
      <DialogContent className="max-h-[90dvh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Override subscription</DialogTitle>
          <DialogDescription>
            This is recorded against your account with the reason you give. Use it for negotiated
            exceptions and incident remediation, not for routine changes.
          </DialogDescription>
        </DialogHeader>

        {problem && !reasonError ? (
          <p
            role="alert"
            className="rounded-xs border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {problem.detail}
          </p>
        ) : null}

        <form
          className="space-y-4"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor={stateId}>State</Label>
              <select
                id={stateId}
                value={state}
                onChange={(event) => setState(event.target.value)}
                className="h-9 w-full rounded-xs border border-input bg-background px-3 text-sm"
              >
                <option value="">Leave unchanged</option>
                {SUBSCRIPTION_STATES.map((value) => (
                  <option key={value} value={value}>
                    {value.replace(/_/g, " ")}
                  </option>
                ))}
              </select>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor={expiresId}>Expires at</Label>
              <Input
                id={expiresId}
                type="date"
                value={expiresAt}
                onChange={(event) => setExpiresAt(event.target.value)}
                aria-describedby={`${expiresId}-hint`}
              />
              <p id={`${expiresId}-hint`} className="text-xs text-muted-foreground">
                Leave empty to keep the current expiry.
              </p>
            </div>
          </div>

          <div className="surface-muted flex items-center justify-between gap-3 px-3 py-2">
            <Label htmlFor={entitlementsId} className="text-sm font-normal">
              Also pin the entitlements to a custom set
            </Label>
            <Switch
              id={entitlementsId}
              checked={editEntitlements}
              onCheckedChange={setEditEntitlements}
            />
          </div>

          {editEntitlements && entitlements ? (
            <fieldset className="space-y-3 border border-border p-3">
              <legend className="field-label px-1">Entitlements</legend>
              <p className="text-xs text-muted-foreground">
                Empty means unlimited; 0 means none. Every field is sent, because a partial map is
                refused.
              </p>
              <div className="grid gap-3 sm:grid-cols-3">
                <NumberOrNull
                  label="Max jobs"
                  value={entitlements.maxJobs}
                  onChange={(value) => setLimit("maxJobs", value)}
                />
                <NumberOrNull
                  label="Max recruiters"
                  value={entitlements.maxRecruiters}
                  onChange={(value) => setLimit("maxRecruiters", value)}
                />
                <NumberOrNull
                  label="Applications / month"
                  value={entitlements.maxApplicationsPerMonth}
                  onChange={(value) => setLimit("maxApplicationsPerMonth", value)}
                />
              </div>
              <div className="grid gap-2 sm:grid-cols-3">
                <Toggle
                  label="Network"
                  checked={entitlements.canPublishToNetwork}
                  onChange={(value) => setLimit("canPublishToNetwork", value)}
                />
                <Toggle
                  label="Talent search"
                  checked={entitlements.canUseTalentSearch}
                  onChange={(value) => setLimit("canUseTalentSearch", value)}
                />
                <Toggle
                  label="Messaging"
                  checked={entitlements.canUseMessaging}
                  onChange={(value) => setLimit("canUseMessaging", value)}
                />
              </div>
            </fieldset>
          ) : null}

          <div className="space-y-1.5">
            <Label htmlFor={reasonId}>
              Reason<span aria-hidden> *</span>
            </Label>
            <Textarea
              id={reasonId}
              rows={3}
              required
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              placeholder="Compensation for the 12 March outage, agreed with the account manager."
              aria-invalid={reasonError ? true : undefined}
              aria-describedby={`${reasonId}-help`}
              className={cn(reasonError && "border-destructive")}
            />
            <p
              id={`${reasonId}-help`}
              className={cn("text-xs", reasonError ? "text-destructive" : "text-muted-foreground")}
            >
              {reasonError ?? "Stored in the audit trail beside your account id."}
            </p>
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose} disabled={saving}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving || tooShort}>
              {saving ? <Loader2 className="animate-spin" aria-hidden /> : null}
              Apply override
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** A limit input where an empty box means "unlimited" rather than "zero". */
function NumberOrNull({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number | null;
  onChange: (value: number | null) => void;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs">
        {label}
      </Label>
      <Input
        id={id}
        inputMode="numeric"
        placeholder="Unlimited"
        value={value === null ? "" : String(value)}
        onChange={(event) => {
          const raw = event.target.value.trim();
          onChange(raw === "" ? null : Math.max(0, Math.trunc(Number(raw) || 0)));
        }}
      />
    </div>
  );
}

function Toggle({
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
    <div className="flex items-center justify-between gap-2 border border-border px-2 py-1.5">
      <Label htmlFor={id} className="text-xs font-normal">
        {label}
      </Label>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </div>
  );
}
