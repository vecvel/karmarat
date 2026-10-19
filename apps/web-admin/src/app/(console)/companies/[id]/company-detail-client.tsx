"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { ArrowLeft, Ban, CheckCircle2, Lock, Trash2 } from "lucide-react";
import { Can, useApi } from "@karmarat/ui/react";

import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
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
import { Field, MonoId, PageHeader, StateBadge } from "@/components/console/primitives";
import { CardSkeleton, EmptyState, ProblemView } from "@/components/console/states";
import { formatDate, formatDateTime, formatMoney, formatNumber, humanise } from "@/lib/format";
import type { CompanyDetail, PlatformCompany } from "@/lib/types";
import { useResource } from "@/lib/use-resource";

export function CompanyDetailClient({ companyId }: { companyId: string }) {
  const router = useRouter();
  const api = useApi();

  const detail = useResource<CompanyDetail>(`/admin/companies/${companyId}`);
  // The companies service holds the record the platform *administers*: the
  // suspension reason, the owner, the internal notes. The admin projection
  // holds the roll-up. Both are needed on this page and neither is the other.
  const record = useResource<PlatformCompany>(`/platform/companies/${companyId}`);

  const [approving, setApproving] = useState(false);
  const [suspending, setSuspending] = useState(false);
  const [deleting, setDeleting] = useState(false);

  function refreshAll() {
    detail.reload();
    record.reload();
  }

  if (detail.error) {
    return (
      <>
        <BackLink />
        <ProblemView problem={detail.error} onRetry={detail.reload} />
      </>
    );
  }

  if (!detail.data) {
    return (
      <>
        <BackLink />
        <CardSkeleton count={4} />
      </>
    );
  }

  const company = detail.data.company;
  const suspended = company.state === "suspended";

  return (
    <>
      <BackLink />

      <PageHeader
        title={company.name || company.slug}
        description={`${company.slug}${company.industry ? ` · ${company.industry}` : ""}${
          company.country ? ` · ${company.country}` : ""
        }`}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <StateBadge state={company.state} />

            <Can permission="platform_companies.approve">
              <Button
                type="button"
                size="sm"
                variant="success"
                onClick={() => setApproving(true)}
                disabled={company.state === "active"}
              >
                <CheckCircle2 aria-hidden />
                Approve
              </Button>
            </Can>

            <Can permission="platform_companies.suspend">
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => setSuspending(true)}
                disabled={suspended}
              >
                <Ban aria-hidden />
                Suspend
              </Button>
            </Can>

            <Can permission="platform_companies.delete">
              <Button type="button" size="sm" variant="destructive" onClick={() => setDeleting(true)}>
                <Trash2 aria-hidden />
                Delete
              </Button>
            </Can>
          </div>
        }
      />

      {suspended && record.data?.suspensionReason ? (
        <div role="status" className="surface border-destructive/40 p-4">
          <p className="field-label text-destructive">Suspended</p>
          <p className="mt-1 text-sm">{record.data.suspensionReason}</p>
          {company.suspendedAt ? (
            <p className="mt-1 text-xs text-muted-foreground">
              Since {formatDateTime(company.suspendedAt)}
            </p>
          ) : null}
        </div>
      ) : null}

      <section aria-labelledby="tenant-facts" className="surface p-4">
        <h2 id="tenant-facts" className="section-title mb-4">
          Tenant
        </h2>
        <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Field label="Company id">
            <MonoId value={company.id} />
          </Field>
          <Field label="Registered">{formatDateTime(company.registeredAt)}</Field>
          <Field label="Approved">{formatDateTime(company.approvedAt)}</Field>
          <Field label="Contact">
            {company.contactEmail ? (
              <a href={`mailto:${company.contactEmail}`} className="underline underline-offset-2">
                {company.contactEmail}
              </a>
            ) : (
              "—"
            )}
          </Field>
          <Field label="Owner">
            {record.data?.ownerName || record.data?.ownerEmail || "—"}
            {record.data?.ownerEmail && record.data?.ownerName ? (
              <div className="text-xs text-muted-foreground">{record.data.ownerEmail}</div>
            ) : null}
          </Field>
          <Field label="Published jobs">{formatNumber(company.usage.publishedJobs)}</Field>
          <Field label="Applications">{formatNumber(company.usage.applications)}</Field>
          <Field label="Open tickets">{formatNumber(company.openTickets)}</Field>
        </dl>
      </section>

      <section aria-labelledby="tenant-billing" className="surface p-4">
        <h2 id="tenant-billing" className="section-title mb-4">
          Billing
        </h2>
        {company.subscription ? (
          <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Field label="Plan">
              {company.subscription.planName || company.subscription.planId}
            </Field>
            <Field label="State">
              <StateBadge state={company.subscription.state} />
            </Field>
            <Field label="Price">
              {formatMoney(
                company.subscription.price.amountMinor,
                company.subscription.price.currency,
              )}
              {company.subscription.price.intervalMonths
                ? ` every ${company.subscription.price.intervalMonths} month${
                    company.subscription.price.intervalMonths === 1 ? "" : "s"
                  }`
                : ""}
            </Field>
            <Field label="Expires">{formatDate(company.subscription.expiresAt)}</Field>
            <Field label="Subscription">
              <Link
                href={`/subscriptions?companyId=${company.id}`}
                className="underline underline-offset-2"
              >
                Open in subscriptions
              </Link>
            </Field>
          </dl>
        ) : (
          <p className="text-sm text-muted-foreground">
            This tenant has no subscription.{" "}
            <Can permission="subscriptions.create">
              <Link href={`/subscriptions?companyId=${company.id}`} className="underline underline-offset-2">
                Create one
              </Link>
            </Can>
          </p>
        )}
      </section>

      <section aria-labelledby="tenant-payments" className="surface p-4">
        <h2 id="tenant-payments" className="section-title mb-4">
          Payments
        </h2>
        {/* null is "withheld", an empty array is "none" — and those are
            different answers an operator deserves to be able to tell apart. */}
        {detail.data.payments === null ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Lock className="size-4" aria-hidden />
            Payment history is withheld: your role does not include payments.read.
          </p>
        ) : detail.data.payments.length === 0 ? (
          <p className="text-sm text-muted-foreground">This tenant has never paid.</p>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <caption className="sr-only">Payments by this company</caption>
              <TableHeader>
                <TableRow>
                  <TableHead scope="col">Paid</TableHead>
                  <TableHead scope="col">Amount</TableHead>
                  <TableHead scope="col">Status</TableHead>
                  <TableHead scope="col">Detail</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {detail.data.payments.map((payment) => (
                  <TableRow key={payment.id}>
                    <TableCell className="text-sm">{formatDateTime(payment.paidAt)}</TableCell>
                    <TableCell className="tabular text-sm">
                      {formatMoney(payment.amountMinor, payment.currency)}
                    </TableCell>
                    <TableCell>
                      <StateBadge state={payment.status} />
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {payment.failureReason || (
                        <Link href={`/payments/${payment.id}`} className="underline underline-offset-2">
                          View payment
                        </Link>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </section>

      <div className="grid gap-4 lg:grid-cols-2">
        <section aria-labelledby="tenant-tickets" className="surface p-4">
          <h2 id="tenant-tickets" className="section-title mb-3">
            Support tickets
          </h2>
          {detail.data.tickets.length === 0 ? (
            <p className="text-sm text-muted-foreground">No tickets from this tenant.</p>
          ) : (
            <ul className="divide-y divide-border">
              {detail.data.tickets.map((ticket) => (
                <li key={ticket.id} className="flex items-start justify-between gap-3 py-2">
                  <div className="min-w-0">
                    <Link
                      href={`/support/${ticket.id}`}
                      className="block truncate text-sm font-medium underline-offset-2 hover:underline"
                    >
                      {ticket.subject}
                    </Link>
                    <p className="text-xs text-muted-foreground">
                      {humanise(ticket.priority)} · opened {formatDate(ticket.openedAt)}
                    </p>
                  </div>
                  <StateBadge state={ticket.status} />
                </li>
              ))}
            </ul>
          )}
        </section>

        <section aria-labelledby="tenant-activity" className="surface p-4">
          <h2 id="tenant-activity" className="section-title mb-3">
            Recent activity
          </h2>
          {detail.data.activity.length === 0 ? (
            <p className="text-sm text-muted-foreground">Nothing recorded for this tenant yet.</p>
          ) : (
            <>
              <ul className="divide-y divide-border">
                {detail.data.activity.slice(0, 8).map((event) => (
                  <li key={event.id} className="py-2">
                    <p className="text-sm">
                      <span className="font-medium">{humanise(event.domain)}</span>{" "}
                      {humanise(event.action).toLowerCase()}
                    </p>
                    <p className="text-xs text-muted-foreground">
                      {formatDateTime(event.occurredAt)}
                    </p>
                  </li>
                ))}
              </ul>
              <Separator className="my-3" />
              <Link
                href={`/activity?company=${company.id}`}
                className="text-sm underline underline-offset-2"
              >
                See the full feed for this tenant
              </Link>
            </>
          )}
        </section>
      </div>

      <ConfirmAction
        open={approving}
        onOpenChange={setApproving}
        title={`Approve ${company.name || company.slug}?`}
        description={
          <>
            <p>
              Approving opens this tenant&rsquo;s portal and lets its recruiters sign in and publish
              jobs.
            </p>
            <p>Their careers site becomes reachable immediately.</p>
          </>
        }
        confirmLabel="Approve company"
        onConfirm={async () => {
          await api.post(`/platform/companies/${company.id}/approve`, {});
          toast.success("Company approved");
          refreshAll();
        }}
      />

      <ConfirmAction
        open={suspending}
        onOpenChange={setSuspending}
        title={`Suspend ${company.name || company.slug}?`}
        description={
          <>
            <p>
              Suspending locks every user of this tenant out of their portal and takes their careers
              site offline.
            </p>
            <p>The reason is stored on the record so support can answer &ldquo;why&rdquo;.</p>
          </>
        }
        confirmLabel="Suspend company"
        destructive
        reason={{
          label: "Reason for suspension",
          placeholder: "Payment dispute, terms violation, customer request…",
          minLength: 3,
        }}
        onConfirm={async (reason) => {
          await api.post(`/platform/companies/${company.id}/suspend`, { reason });
          toast.success("Company suspended");
          refreshAll();
        }}
      />

      <ConfirmAction
        open={deleting}
        onOpenChange={setDeleting}
        title={`Delete ${company.name || company.slug}?`}
        description={
          <>
            <p>
              The tenant stops being served: no sign-in, no careers page, no API access. The record
              is kept so support and finance can still investigate.
            </p>
            <p>This console offers no way to undo it.</p>
          </>
        }
        confirmLabel="Delete company"
        destructive
        onConfirm={async () => {
          await api.delete(`/platform/companies/${company.id}`);
          toast.success("Company deleted");
          router.push("/companies");
        }}
      />

      {record.error && record.error.status !== 403 ? (
        <EmptyState
          title="Some tenant details could not be loaded"
          description={record.error.detail}
          action={
            <Button type="button" variant="outline" size="sm" onClick={record.reload}>
              Try again
            </Button>
          }
        />
      ) : null}
    </>
  );
}

function BackLink() {
  return (
    <Link
      href="/companies"
      className="inline-flex w-fit items-center gap-1.5 text-sm text-muted-foreground underline-offset-2 hover:underline"
    >
      <ArrowLeft className="size-4" aria-hidden />
      All companies
    </Link>
  );
}
