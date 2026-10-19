"use client";

import Link from "next/link";
import { useId, useMemo, useState } from "react";
import { CreditCard, Plus, ShieldAlert, Trash2 } from "lucide-react";
import { Can, useApi } from "@karmarat/ui/react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { toast } from "@/components/ui/sonner";
import { ConfirmAction } from "@/components/console/confirm-action";
import { CursorPager, MonoId, PageHeader, StateBadge } from "@/components/console/primitives";
import { EmptyState, ProblemView, TableSkeleton } from "@/components/console/states";
import { formatDate, formatInterval, formatMoney } from "@/lib/format";
import { SUBSCRIPTION_STATES, type Plan, type Subscription } from "@/lib/types";
import { useCursorList } from "@/lib/use-cursor-list";
import { useResource } from "@/lib/use-resource";
import { CreateSubscriptionDialog } from "./create-subscription";
import { OverrideDialog } from "./override-dialog";

export function SubscriptionsClient({
  initialCompanyId,
  initialState,
}: {
  initialCompanyId: string;
  initialState: string;
}) {
  const api = useApi();
  const companyId = useId();
  const stateId = useId();

  const [companyFilterInput, setCompanyFilterInput] = useState(initialCompanyId);
  const [companyFilter, setCompanyFilter] = useState(initialCompanyId);
  const [state, setState] = useState(initialState);

  const [creating, setCreating] = useState(false);
  const [overriding, setOverriding] = useState<Subscription | null>(null);
  const [deleting, setDeleting] = useState<Subscription | null>(null);

  const query = useMemo(
    () => ({ companyId: companyFilter, state }),
    [companyFilter, state],
  );
  // The subscriptions list is the one endpoint on the platform that returns its
  // rows under `data` rather than `items`; naming the key here rather than
  // guessing means a change shows up as an empty table, not a crash.
  const list = useCursorList<Subscription>("/subscriptions", query, { itemsKey: "data" });

  const plans = useResource<{ items: Plan[] }>("/plans?limit=100");
  const planNames = useMemo(() => {
    const map = new Map<string, Plan>();
    for (const plan of plans.data?.items ?? []) map.set(plan.id, plan);
    return map;
  }, [plans.data]);

  return (
    <>
      <PageHeader
        title="Subscriptions"
        description="Every tenant's billing state. An override is audited and needs a reason."
        actions={
          <Can permission="subscriptions.create">
            <Button type="button" size="sm" onClick={() => setCreating(true)}>
              <Plus aria-hidden />
              New subscription
            </Button>
          </Can>
        }
      />

      <form
        className="surface flex flex-wrap items-end gap-3 p-3"
        onSubmit={(event) => {
          event.preventDefault();
          setCompanyFilter(companyFilterInput.trim());
        }}
      >
        <div className="min-w-[18rem] flex-1 space-y-1.5">
          <Label htmlFor={companyId}>Company id</Label>
          <Input
            id={companyId}
            value={companyFilterInput}
            placeholder="UUID of the tenant"
            onChange={(event) => setCompanyFilterInput(event.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={stateId}>State</Label>
          <select
            id={stateId}
            value={state}
            onChange={(event) => setState(event.target.value)}
            className="h-9 w-44 rounded-xs border border-input bg-background px-3 text-sm"
          >
            <option value="">All states</option>
            {SUBSCRIPTION_STATES.map((value) => (
              <option key={value} value={value}>
                {value.replace(/_/g, " ")}
              </option>
            ))}
          </select>
        </div>
        <Button type="submit" size="sm">
          Apply
        </Button>
        {(companyFilter || state) && (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={() => {
              setCompanyFilter("");
              setCompanyFilterInput("");
              setState("");
            }}
          >
            Clear
          </Button>
        )}
      </form>

      {list.loading && list.items.length === 0 ? <TableSkeleton rows={6} columns={6} /> : null}
      {list.error ? <ProblemView problem={list.error} onRetry={list.reload} /> : null}

      {!list.loading && !list.error && list.items.length === 0 ? (
        <EmptyState
          icon={<CreditCard className="size-5" aria-hidden />}
          title="No subscriptions match"
          description={
            companyFilter || state
              ? "Nothing matched these filters."
              : "No tenant has a subscription yet."
          }
        />
      ) : null}

      {list.items.length > 0 ? (
        <div className="surface overflow-x-auto">
          <Table>
            <caption className="sr-only">Subscriptions across every tenant</caption>
            <TableHeader>
              <TableRow>
                <TableHead scope="col">Company</TableHead>
                <TableHead scope="col">Plan</TableHead>
                <TableHead scope="col">State</TableHead>
                <TableHead scope="col">Price</TableHead>
                <TableHead scope="col">Period</TableHead>
                <TableHead scope="col" className="text-right">
                  Actions
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.items.map((subscription) => (
                <TableRow key={subscription.id}>
                  <TableCell>
                    <Link
                      href={`/companies/${subscription.companyId}`}
                      className="text-sm underline-offset-2 hover:underline"
                    >
                      <MonoId value={subscription.companyId} />
                    </Link>
                  </TableCell>
                  <TableCell className="text-sm">
                    {planNames.get(subscription.planId)?.name ?? (
                      <MonoId value={subscription.planId} />
                    )}
                  </TableCell>
                  <TableCell>
                    <StateBadge state={subscription.state} />
                    {subscription.cancelAtPeriodEnd ? (
                      <div className="text-xs text-warning">Cancels at period end</div>
                    ) : null}
                  </TableCell>
                  <TableCell className="tabular text-sm">
                    {formatMoney(subscription.price.amount, subscription.price.currency)}
                    <div className="text-xs text-muted-foreground">
                      {formatInterval(subscription.price.interval, subscription.price.intervalCount)}
                    </div>
                  </TableCell>
                  <TableCell className="text-sm text-muted-foreground">
                    {subscription.currentPeriodEnd
                      ? `until ${formatDate(subscription.currentPeriodEnd)}`
                      : subscription.expiresAt
                        ? `expires ${formatDate(subscription.expiresAt)}`
                        : "no end date"}
                    {subscription.trialEndsAt ? (
                      <div className="text-xs">Trial to {formatDate(subscription.trialEndsAt)}</div>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end gap-1.5">
                      <Can permission="subscriptions.override">
                        <Button
                          type="button"
                          size="xs"
                          variant="outline"
                          onClick={() => setOverriding(subscription)}
                        >
                          <ShieldAlert aria-hidden />
                          Override
                        </Button>
                      </Can>
                      <Can permission="subscriptions.delete">
                        <Button
                          type="button"
                          size="icon-xs"
                          variant="ghost"
                          className="text-destructive hover:bg-destructive/10"
                          onClick={() => setDeleting(subscription)}
                        >
                          <Trash2 aria-hidden />
                          <span className="sr-only">Delete subscription</span>
                        </Button>
                      </Can>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}

      <CursorPager
        history={list.history}
        nextCursor={list.nextCursor}
        onBack={list.back}
        onNext={list.next}
        loading={list.loading}
        count={list.items.length}
      />

      <CreateSubscriptionDialog
        open={creating}
        onOpenChange={setCreating}
        plans={plans.data?.items ?? []}
        defaultCompanyId={companyFilter}
        onCreated={() => {
          toast.success("Subscription created");
          list.reload();
        }}
      />

      <OverrideDialog
        subscription={overriding}
        onClose={() => setOverriding(null)}
        onSaved={() => {
          toast.success("Override applied and recorded");
          list.reload();
        }}
      />

      <ConfirmAction
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title="Delete this subscription?"
        description={
          <>
            <p>
              The tenant loses its entitlements immediately: jobs stop being publishable and the
              portal falls back to the no-plan floor.
            </p>
            <p>Cancel it instead if the customer is simply leaving at the end of their period.</p>
          </>
        }
        confirmLabel="Delete subscription"
        destructive
        onConfirm={async () => {
          if (!deleting) return;
          await api.delete(`/subscriptions/${deleting.id}`);
          toast.success("Subscription deleted");
          setDeleting(null);
          list.reload();
        }}
      />
    </>
  );
}
