"use client";

import { useId, useState, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import type { ProblemError } from "@karmarat/ui";

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { asProblem } from "@/lib/use-resource";
import { cn } from "@/lib/utils";

/**
 * A confirmation step for the actions that change whether a customer can work.
 *
 * Approving and suspending a tenant, refunding a payment and deleting a plan
 * are not undo-able from this console, so none of them happen on a single
 * click. Where the API records a reason — suspension, a subscription override —
 * the field is here and required, because the audit row is only useful if
 * somebody had to write in it.
 */
export function ConfirmAction({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  destructive = false,
  reason,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: ReactNode;
  confirmLabel: string;
  destructive?: boolean;
  /** Set when the API requires a reason; `minLength` mirrors its validation. */
  reason?: { label: string; placeholder?: string; minLength?: number; required?: boolean };
  onConfirm: (reason: string) => Promise<void>;
}) {
  const reasonId = useId();
  const [value, setValue] = useState("");
  const [problem, setProblem] = useState<ProblemError | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const minLength = reason?.minLength ?? 3;
  const required = reason?.required ?? true;
  const tooShort = Boolean(reason) && required && value.trim().length < minLength;

  async function confirm() {
    setSubmitting(true);
    setProblem(null);
    try {
      await onConfirm(value.trim());
      setValue("");
      onOpenChange(false);
    } catch (caught) {
      setProblem(asProblem(caught));
    } finally {
      setSubmitting(false);
    }
  }

  // The server's field message wins over the local length hint.
  const fieldError = problem?.fieldError("reason");

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (submitting) return;
        if (!next) {
          setValue("");
          setProblem(null);
        }
        onOpenChange(next);
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="space-y-2 text-sm text-muted-foreground">{description}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>

        {reason ? (
          <div className="space-y-1.5">
            <Label htmlFor={reasonId}>
              {reason.label}
              {required ? <span aria-hidden> *</span> : null}
            </Label>
            <Textarea
              id={reasonId}
              value={value}
              onChange={(event) => setValue(event.target.value)}
              placeholder={reason.placeholder}
              rows={3}
              required={required}
              aria-invalid={fieldError ? true : undefined}
              aria-describedby={`${reasonId}-help`}
              className={cn(fieldError && "border-destructive")}
            />
            <p
              id={`${reasonId}-help`}
              className={cn("text-xs", fieldError ? "text-destructive" : "text-muted-foreground")}
            >
              {fieldError ?? `Stored on the record and shown to support. At least ${minLength} characters.`}
            </p>
          </div>
        ) : null}

        {problem && !fieldError ? (
          <p
            role="alert"
            className="rounded-xs border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            {problem.detail}
          </p>
        ) : null}

        <AlertDialogFooter>
          <AlertDialogCancel disabled={submitting}>Cancel</AlertDialogCancel>
          <Button
            type="button"
            variant={destructive ? "destructive" : "default"}
            onClick={() => void confirm()}
            disabled={submitting || tooShort}
          >
            {submitting ? <Loader2 className="animate-spin" aria-hidden /> : null}
            {confirmLabel}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
