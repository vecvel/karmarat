"use client";

import { useMemo, useState } from "react";
import { Archive, Infinity as InfinityIcon, Pencil, Plus, Send, Tags, Trash2 } from "lucide-react";
import { Can, useApi } from "@karmarat/ui/react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { toast } from "@/components/ui/sonner";
import { ConfirmAction } from "@/components/console/confirm-action";
import { CursorPager, PageHeader, StateBadge } from "@/components/console/primitives";
import { EmptyState, ProblemView, TableSkeleton } from "@/components/console/states";
import { formatInterval, formatMoney, formatNumber, humanise } from "@/lib/format";
import type { Entitlements, Plan } from "@/lib/types";
import { useCursorList } from "@/lib/use-cursor-list";
import { PlanEditor } from "./plan-editor";

const PLAN_STATES = ["draft", "published", "retired"];

export function PlansClient() {
  const api = useApi();
  const [state, setState] = useState("");
  const [editing, setEditing] = useState<Plan | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [publishing, setPublishing] = useState<Plan | null>(null);
  const [retiring, setRetiring] = useState<Plan | null>(null);
  const [deleting, setDeleting] = useState<Plan | null>(null);

  const query = useMemo(() => ({ state }), [state]);
  const list = useCursorList<Plan>("/plans", query, { limit: 50 });

  return (
    <>
      <PageHeader
        title="Plans"
        description="The catalogue every tenant subscribes from. A plan is only offered once it is published."
        actions={
          <Can permission="plans.create">
            <Button
              type="button"
              size="sm"
              onClick={() => {
                setEditing(null);
                setEditorOpen(true);
              }}
            >
              <Plus aria-hidden />
              New plan
            </Button>
          </Can>
        }
      />

      <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Filter by state">
        <Button
          type="button"
          size="sm"
          variant={state === "" ? "default" : "outline"}
          onClick={() => setState("")}
          aria-pressed={state === ""}
        >
          All
        </Button>
        {PLAN_STATES.map((value) => (
          <Button
            key={value}
            type="button"
            size="sm"
            variant={state === value ? "default" : "outline"}
            onClick={() => setState(value)}
            aria-pressed={state === value}
          >
            {humanise(value)}
          </Button>
        ))}
      </div>

      {list.loading && list.items.length === 0 ? <TableSkeleton rows={4} columns={4} /> : null}
      {list.error ? <ProblemView problem={list.error} onRetry={list.reload} /> : null}

      {!list.loading && !list.error && list.items.length === 0 ? (
        <EmptyState
          icon={<Tags className="size-5" aria-hidden />}
          title={state ? `No ${state} plans` : "No plans yet"}
          description={
            state
              ? "Nothing in the catalogue is in this state."
              : "Create a plan to start offering subscriptions. Nothing is sold until it is published."
          }
          action={
            <Can permission="plans.create">
              <Button
                type="button"
                size="sm"
                onClick={() => {
                  setEditing(null);
                  setEditorOpen(true);
                }}
              >
                <Plus aria-hidden />
                New plan
              </Button>
            </Can>
          }
        />
      ) : null}

      <div className="grid gap-3 lg:grid-cols-2">
        {list.items.map((plan) => (
          <article key={plan.id} className="surface flex flex-col gap-4 p-4">
            <header className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <h2 className="truncate text-base font-semibold">{plan.name}</h2>
                <p className="font-mono text-xs text-muted-foreground">{plan.key}</p>
              </div>
              <StateBadge state={plan.state} />
            </header>

            <div>
              <p className="tabular text-2xl font-semibold">
                {formatMoney(plan.price.amount, plan.price.currency)}
              </p>
              <p className="text-xs text-muted-foreground">
                {formatInterval(plan.interval, plan.intervalCount)}
                {plan.trialDays > 0 ? ` · ${plan.trialDays}-day trial` : ""}
              </p>
            </div>

            {plan.description ? (
              <p className="text-sm text-muted-foreground">{plan.description}</p>
            ) : null}

            <EntitlementSummary entitlements={plan.entitlements} />

            <footer className="mt-auto flex flex-wrap items-center gap-2 border-t border-border pt-3">
              <Can permission="plans.update">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setEditing(plan);
                    setEditorOpen(true);
                  }}
                >
                  <Pencil aria-hidden />
                  Edit
                </Button>
              </Can>

              <Can permission="plans.publish">
                {plan.state === "published" ? (
                  <Button type="button" size="sm" variant="outline" onClick={() => setRetiring(plan)}>
                    <Archive aria-hidden />
                    Retire
                  </Button>
                ) : (
                  <Button
                    type="button"
                    size="sm"
                    variant="accent"
                    onClick={() => setPublishing(plan)}
                    disabled={plan.state === "retired"}
                  >
                    <Send aria-hidden />
                    Publish
                  </Button>
                )}
              </Can>

              <Can permission="plans.delete">
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="ml-auto text-destructive hover:bg-destructive/10"
                  onClick={() => setDeleting(plan)}
                >
                  <Trash2 aria-hidden />
                  Delete
                </Button>
              </Can>
            </footer>
          </article>
        ))}
      </div>

      <CursorPager
        history={list.history}
        nextCursor={list.nextCursor}
        onBack={list.back}
        onNext={list.next}
        loading={list.loading}
        count={list.items.length}
      />

      <PlanEditor
        open={editorOpen}
        onOpenChange={setEditorOpen}
        plan={editing}
        onSaved={() => {
          toast.success(editing ? "Plan updated" : "Plan created");
          list.reload();
        }}
      />

      <ConfirmAction
        open={publishing !== null}
        onOpenChange={(open) => !open && setPublishing(null)}
        title={publishing ? `Publish ${publishing.name}?` : "Publish plan"}
        description={
          <p>
            A published plan appears on the public pricing page and can be subscribed to
            immediately.
          </p>
        }
        confirmLabel="Publish"
        onConfirm={async () => {
          if (!publishing) return;
          await api.post(`/plans/${publishing.id}/publish`, {});
          toast.success("Plan published");
          setPublishing(null);
          list.reload();
        }}
      />

      <ConfirmAction
        open={retiring !== null}
        onOpenChange={(open) => !open && setRetiring(null)}
        title={retiring ? `Retire ${retiring.name}?` : "Retire plan"}
        description={
          <>
            <p>The plan leaves the pricing page and no new tenant can subscribe to it.</p>
            <p>Tenants already on it keep their subscription.</p>
          </>
        }
        confirmLabel="Retire"
        reason={{ label: "Reason", required: false, minLength: 0 }}
        onConfirm={async (reason) => {
          if (!retiring) return;
          await api.post(`/plans/${retiring.id}/retire`, reason ? { reason } : {});
          toast.success("Plan retired");
          setRetiring(null);
          list.reload();
        }}
      />

      <ConfirmAction
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={deleting ? `Delete ${deleting.name}?` : "Delete plan"}
        description={
          <p>
            The plan is removed from the catalogue. If any tenant is subscribed to it the API will
            refuse — retire it instead.
          </p>
        }
        confirmLabel="Delete plan"
        destructive
        onConfirm={async () => {
          if (!deleting) return;
          await api.delete(`/plans/${deleting.id}`);
          toast.success("Plan deleted");
          setDeleting(null);
          list.reload();
        }}
      />
    </>
  );
}

/**
 * The entitlement map at a glance.
 *
 * "Unlimited" and "None" are spelled out rather than shown as `null` and `0`:
 * they are the two answers an operator most needs to tell apart, and a bare
 * zero in a table reads as "not configured".
 */
function EntitlementSummary({ entitlements }: { entitlements: Entitlements }) {
  const limits: [string, number | null][] = [
    ["Jobs", entitlements.maxJobs],
    ["Recruiters", entitlements.maxRecruiters],
    ["Applications / month", entitlements.maxApplicationsPerMonth],
  ];

  const flags: [string, boolean][] = [
    ["Network", entitlements.canPublishToNetwork],
    ["Talent search", entitlements.canUseTalentSearch],
    ["Messaging", entitlements.canUseMessaging],
  ];

  return (
    <div className="space-y-2">
      <dl className="grid grid-cols-3 gap-2 text-xs">
        {limits.map(([label, value]) => (
          <div key={label} className="surface-muted px-2 py-1.5">
            <dt className="text-[10px] uppercase tracking-wide text-muted-foreground">{label}</dt>
            <dd className="tabular flex items-center gap-1 font-medium">
              {value === null ? (
                <>
                  <InfinityIcon className="size-3.5" aria-hidden />
                  <span className="sr-only">Unlimited</span>
                  <span aria-hidden>Unlimited</span>
                </>
              ) : value === 0 ? (
                <span className="text-muted-foreground">None</span>
              ) : (
                formatNumber(value)
              )}
            </dd>
          </div>
        ))}
      </dl>
      <div className="flex flex-wrap gap-1.5">
        {flags.map(([label, enabled]) => (
          <Badge key={label} variant={enabled ? "soft-success" : "soft-neutral"}>
            {enabled ? label : `No ${label.toLowerCase()}`}
          </Badge>
        ))}
        <Badge variant="outline">{humanise(entitlements.supportTier)} support</Badge>
        <Badge variant="outline">{entitlements.storageGb} GB</Badge>
      </div>
    </div>
  );
}
