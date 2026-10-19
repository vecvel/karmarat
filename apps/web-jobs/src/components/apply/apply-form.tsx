"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import { CheckCircle2 } from "lucide-react";
import { isProblem, ProblemError } from "@karmarat/ui";
import { useApi } from "@karmarat/ui/react";

import { ApplicationFormField } from "@/components/apply/form-field";
import { ProblemAlert } from "@/components/feedback";
import { ResumePicker } from "@/components/resumes/resume-picker";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import type { Application, FormField, JobFormResponse } from "@/lib/api-types";
import {
  toAnswerPayload,
  validateAnswer,
  type AnswerValue,
} from "@/lib/apply-validation";

function initialValue(field: FormField): AnswerValue {
  if (field.type === "boolean") return false;
  if (field.type === "multi_select") return [];
  return "";
}

export function ApplyForm({
  jobId,
  slug,
  title,
  form,
}: {
  jobId: string;
  slug: string;
  title: string;
  form: JobFormResponse["form"];
}) {
  const api = useApi();
  const router = useRouter();

  const fields = useMemo(() => form.fields ?? [], [form.fields]);
  const [values, setValues] = useState<Record<string, AnswerValue>>(() =>
    Object.fromEntries(fields.map((field) => [field.key, initialValue(field)])),
  );
  const [resumeKey, setResumeKey] = useState<string | null>(null);
  const [localErrors, setLocalErrors] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<unknown>(null);
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState<Application | null>(null);

  // The server's field messages take precedence over this app's: the service
  // validates against the form it fetches at submission time, which may have
  // changed since this page loaded, and it is the copy that counts.
  const serverErrors =
    failure instanceof ProblemError ? failure.fieldErrors : {};

  const errorFor = (key: string): string | undefined =>
    serverErrors[key]?.[0] ?? localErrors[key];

  const alreadyApplied = isProblem(failure) && failure.code === "already_applied";

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setFailure(null);

    const problems: Record<string, string> = {};
    for (const field of fields) {
      const message = validateAnswer(field, values[field.key] ?? null);
      if (message) problems[field.key] = message;
    }

    setLocalErrors(problems);
    if (Object.keys(problems).length > 0) {
      // Move the caret to the first problem rather than leaving the person to
      // hunt for it in a twenty-question form.
      const first = fields.find((field) => problems[field.key]);
      if (first) {
        document.getElementById(`field-${first.key}`)?.focus();
      }
      return;
    }

    setSubmitting(true);
    try {
      const application = await api.post<Application>("/api/v1/public/apply", {
        jobId,
        answers: toAnswerPayload(fields, values),
        ...(resumeKey ? { resumeKey } : {}),
        source: "portal",
      });
      setSubmitted(application);
    } catch (cause) {
      setFailure(cause);
      // The alert is above the form; scrolling to it is what makes a rejected
      // submission visible on a long page.
      window.scrollTo({ top: 0, behavior: "smooth" });
    } finally {
      setSubmitting(false);
    }
  };

  if (submitted) {
    return (
      <div className="surface space-y-4 p-6">
        <p className="inline-flex items-center gap-2 text-base font-semibold">
          <CheckCircle2 aria-hidden="true" className="size-5 text-success" />
          Application sent
        </p>
        <p className="text-sm text-muted-foreground">
          {submitted.companyName
            ? `${submitted.companyName} has your application for ${submitted.jobTitle}.`
            : `Your application for ${title} is in.`}{" "}
          You can follow it, and message the company, from your applications
          page.
        </p>
        <div className="flex flex-wrap gap-2">
          <Button asChild variant="accent">
            <Link href="/applications">Track this application</Link>
          </Button>
          <Button asChild variant="outline">
            <Link href="/">Keep looking</Link>
          </Button>
        </div>
      </div>
    );
  }

  if (alreadyApplied) {
    return (
      <div className="surface space-y-4 p-6">
        <p className="text-base font-semibold">You have already applied</p>
        <p className="text-sm text-muted-foreground">
          A company only accepts one application per person per role. Yours is
          already with them.
        </p>
        <div className="flex flex-wrap gap-2">
          <Button asChild variant="accent">
            <Link href="/applications">See your application</Link>
          </Button>
          <Button asChild variant="outline">
            <Link href={`/jobs/${slug}`}>Back to the role</Link>
          </Button>
        </div>
      </div>
    );
  }

  const fileFields = fields.filter((field) => field.type === "file");
  const needsResume = fileFields.length === 0;

  return (
    <form onSubmit={submit} noValidate className="space-y-6">
      {failure ? <ProblemAlert error={failure} /> : null}

      <div className="surface space-y-5 p-5">
        {fields.map((field) => (
          <ApplicationFormField
            key={field.key}
            field={field}
            value={values[field.key] ?? initialValue(field)}
            error={errorFor(field.key)}
            disabled={submitting}
            onChange={(next) => {
              setValues((current) => ({ ...current, [field.key]: next }));
              // Clear this app's own complaint as soon as it is addressed; the
              // server's stays until the next submission answers it.
              setLocalErrors((current) => {
                if (!current[field.key]) return current;
                const { [field.key]: _dropped, ...rest } = current;
                return rest;
              });
            }}
            fileSlot={
              field.type === "file" ? (
                <ResumePicker
                  selectedKey={
                    typeof values[field.key] === "string"
                      ? (values[field.key] as string)
                      : null
                  }
                  onSelect={(key) => {
                    setValues((current) => ({ ...current, [field.key]: key ?? "" }));
                    // The same document answers the form's file question and
                    // the application's own résumé pointer.
                    setResumeKey(key);
                  }}
                />
              ) : undefined
            }
          />
        ))}

        {needsResume ? (
          <>
            <Separator />
            <div className="space-y-2">
              <h2 id="resume-heading" className="text-sm font-medium">
                Résumé <span className="text-muted-foreground">(optional)</span>
              </h2>
              <p className="text-sm text-muted-foreground">
                This role does not ask for one, but attaching it gives the
                recruiter more to go on.
              </p>
              <ResumePicker
                labelledBy="resume-heading"
                selectedKey={resumeKey}
                onSelect={setResumeKey}
              />
            </div>
          </>
        ) : null}
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" variant="accent" disabled={submitting}>
          {submitting ? "Sending…" : "Submit application"}
        </Button>
        <Button asChild variant="ghost" type="button">
          <Link href={`/jobs/${slug}`}>Cancel</Link>
        </Button>
        <p className="text-xs text-muted-foreground" aria-live="polite">
          {submitting ? "Sending your application…" : null}
        </p>
      </div>
    </form>
  );
}
