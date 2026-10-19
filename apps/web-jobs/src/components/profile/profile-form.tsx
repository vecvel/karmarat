"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { ProblemError } from "@karmarat/ui";
import { useApi } from "@karmarat/ui/react";

import { FieldError, ListSkeleton, ProblemAlert } from "@/components/feedback";
import { TagInput } from "@/components/profile/tag-input";
import { useSession } from "@/components/session-provider";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Textarea } from "@/components/ui/textarea";
import type { EmploymentType, Profile, ProfilePatch } from "@/lib/api-types";
import { EMPLOYMENT_TYPES, WORK_AUTHORISATIONS } from "@/lib/format";

export function ProfileForm() {
  const api = useApi();
  const { ready, session } = useSession();

  const [profile, setProfile] = useState<Profile | null>(null);
  const [draft, setDraft] = useState<Profile | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!ready || !session) return;

    let cancelled = false;
    void (async () => {
      try {
        const loaded = await api.get<Profile>("/api/v1/me");
        if (cancelled) return;
        setProfile(loaded);
        setDraft(loaded);
        setLoadError(null);
      } catch (cause) {
        if (!cancelled) setLoadError(cause);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [api, ready, session]);

  const fieldError = (name: string) =>
    saveError instanceof ProblemError ? saveError.fieldError(name) : undefined;

  if (!ready || loading) return <ListSkeleton rows={3} />;
  if (loadError) return <ProblemAlert error={loadError} />;
  if (!draft || !profile) return null;

  const set = <K extends keyof Profile>(key: K, value: Profile[K]) =>
    setDraft((current) => (current ? { ...current, [key]: value } : current));

  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setSaveError(null);

    // PATCH carries only what changed. Sending the whole profile back would
    // overwrite a field another device edited between this page loading and
    // this button being pressed.
    const patch: ProfilePatch = {};
    const assign = <K extends keyof ProfilePatch>(key: K, next: ProfilePatch[K], previous: unknown) => {
      if (JSON.stringify(next) !== JSON.stringify(previous)) patch[key] = next;
    };

    assign("headline", draft.headline, profile.headline);
    assign("summary", draft.summary, profile.summary);
    assign("location", draft.location, profile.location);
    assign("yearsExperience", draft.yearsExperience, profile.yearsExperience);
    assign("currentTitle", draft.currentTitle, profile.currentTitle);
    assign("currentEmployer", draft.currentEmployer, profile.currentEmployer);
    assign("phone", draft.phone, profile.phone);
    assign("skills", draft.skills, profile.skills);
    assign("languages", draft.languages, profile.languages);
    assign("websiteUrl", draft.websiteUrl, profile.websiteUrl);
    assign("linkedinUrl", draft.linkedinUrl, profile.linkedinUrl);
    assign("githubUrl", draft.githubUrl, profile.githubUrl);
    assign("openToTypes", draft.openToTypes, profile.openToTypes);
    assign("openToRemote", draft.openToRemote, profile.openToRemote);
    assign("workAuthorisation", draft.workAuthorisation, profile.workAuthorisation);

    if (
      draft.desiredSalary?.minor !== profile.desiredSalary?.minor ||
      draft.desiredSalary?.currency !== profile.desiredSalary?.currency
    ) {
      patch.desiredSalaryMinor = draft.desiredSalary?.minor ?? null;
      patch.desiredSalaryCurrency = draft.desiredSalary?.currency ?? null;
    }

    if (Object.keys(patch).length === 0) {
      setSaving(false);
      toast.info("Nothing to save — no changes yet.");
      return;
    }

    try {
      const updated = await api.patch<Profile>("/api/v1/me", patch);
      setProfile(updated);
      setDraft(updated);
      toast.success("Profile saved.");
    } catch (cause) {
      setSaveError(cause);
    } finally {
      setSaving(false);
    }
  };

  const salaryMajor =
    draft.desiredSalary?.minor === undefined
      ? ""
      : String(Math.round(draft.desiredSalary.minor / 100));

  return (
    <form onSubmit={save} noValidate className="space-y-6">
      {saveError ? <ProblemAlert error={saveError} /> : null}

      <section className="surface space-y-4 p-5">
        <h2 className="section-title">Who you are</h2>

        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="headline">Headline</Label>
            <Input
              id="headline"
              value={draft.headline}
              placeholder="Senior backend engineer — distributed systems"
              aria-describedby="headline-help"
              aria-invalid={fieldError("headline") ? true : undefined}
              onChange={(event) => set("headline", event.target.value)}
            />
            <p id="headline-help" className="text-xs text-muted-foreground">
              The one line a recruiter reads first.
            </p>
            <FieldError message={fieldError("headline")} />
          </div>

          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="summary">Summary</Label>
            <Textarea
              id="summary"
              rows={5}
              value={draft.summary}
              aria-invalid={fieldError("summary") ? true : undefined}
              onChange={(event) => set("summary", event.target.value)}
            />
            <FieldError message={fieldError("summary")} />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="location">Location</Label>
            <Input
              id="location"
              value={draft.location}
              autoComplete="address-level2"
              onChange={(event) => set("location", event.target.value)}
            />
            <FieldError message={fieldError("location")} />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="phone">Phone</Label>
            <Input
              id="phone"
              type="tel"
              autoComplete="tel"
              value={draft.phone}
              onChange={(event) => set("phone", event.target.value)}
            />
            <FieldError message={fieldError("phone")} />
          </div>
        </div>
      </section>

      <section className="surface space-y-4 p-5">
        <h2 className="section-title">Where you are now</h2>

        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="currentTitle">Current title</Label>
            <Input
              id="currentTitle"
              value={draft.currentTitle}
              onChange={(event) => set("currentTitle", event.target.value)}
            />
            <FieldError message={fieldError("currentTitle")} />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="currentEmployer">Current employer</Label>
            <Input
              id="currentEmployer"
              value={draft.currentEmployer}
              onChange={(event) => set("currentEmployer", event.target.value)}
            />
            <FieldError message={fieldError("currentEmployer")} />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="yearsExperience">Years of experience</Label>
            <Input
              id="yearsExperience"
              type="number"
              min={0}
              max={80}
              value={String(draft.yearsExperience)}
              onChange={(event) =>
                set("yearsExperience", Number(event.target.value) || 0)
              }
            />
            <FieldError message={fieldError("yearsExperience")} />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="workAuthorisation">Work authorisation</Label>
            <select
              id="workAuthorisation"
              className="flex h-10 w-full rounded-xs border border-input bg-transparent px-3 text-sm"
              value={draft.workAuthorisation}
              onChange={(event) =>
                set(
                  "workAuthorisation",
                  event.target.value as Profile["workAuthorisation"],
                )
              }
            >
              {WORK_AUTHORISATIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
            <FieldError message={fieldError("workAuthorisation")} />
          </div>
        </div>

        <Separator />

        <div className="grid gap-4 sm:grid-cols-2">
          <TagInput
            id="skills"
            label="Skills"
            values={draft.skills}
            placeholder="Go, PostgreSQL, distributed systems"
            onChange={(values) => set("skills", values)}
          />
          <TagInput
            id="languages"
            label="Languages"
            values={draft.languages}
            placeholder="English, Spanish"
            onChange={(values) => set("languages", values)}
          />
        </div>
      </section>

      <section className="surface space-y-4 p-5">
        <h2 className="section-title">What you are looking for</h2>

        <fieldset className="space-y-2">
          <legend className="text-sm font-medium">Open to</legend>
          <div className="flex flex-wrap gap-4">
            {EMPLOYMENT_TYPES.map((type) => {
              const checked = draft.openToTypes.includes(type.value);
              return (
                <label key={type.value} className="flex items-center gap-2 text-sm">
                  <Checkbox
                    checked={checked}
                    onCheckedChange={(next) =>
                      set(
                        "openToTypes",
                        next === true
                          ? [...draft.openToTypes, type.value as EmploymentType]
                          : draft.openToTypes.filter((item) => item !== type.value),
                      )
                    }
                  />
                  {type.label}
                </label>
              );
            })}
          </div>
          <FieldError message={fieldError("openToTypes")} />
        </fieldset>

        <label className="flex items-center gap-2 text-sm">
          <Checkbox
            checked={draft.openToRemote}
            onCheckedChange={(next) => set("openToRemote", next === true)}
          />
          Open to fully remote roles
        </label>

        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="desiredSalary">Desired salary</Label>
            <Input
              id="desiredSalary"
              type="number"
              min={0}
              inputMode="numeric"
              value={salaryMajor}
              aria-describedby="desiredSalary-help"
              onChange={(event) => {
                const major = event.target.value;
                set(
                  "desiredSalary",
                  major === ""
                    ? undefined
                    : {
                        // Money is stored in minor units so no binary fraction
                        // can round anyone's salary expectation.
                        minor: Math.round(Number(major) * 100),
                        currency: draft.desiredSalary?.currency || "USD",
                      },
                );
              }}
            />
            <p id="desiredSalary-help" className="text-xs text-muted-foreground">
              Annual, before tax. Only shared with companies you apply to.
            </p>
            <FieldError message={fieldError("desiredSalaryMinor")} />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="desiredCurrency">Currency</Label>
            <Input
              id="desiredCurrency"
              maxLength={3}
              placeholder="USD"
              value={draft.desiredSalary?.currency ?? ""}
              onChange={(event) =>
                set("desiredSalary", {
                  minor: draft.desiredSalary?.minor ?? 0,
                  currency: event.target.value.toUpperCase(),
                })
              }
            />
            <FieldError message={fieldError("desiredSalaryCurrency")} />
          </div>
        </div>
      </section>

      <section className="surface space-y-4 p-5">
        <h2 className="section-title">Links</h2>
        <div className="grid gap-4 sm:grid-cols-3">
          <div className="space-y-1.5">
            <Label htmlFor="websiteUrl">Website</Label>
            <Input
              id="websiteUrl"
              type="url"
              placeholder="https://"
              value={draft.websiteUrl}
              onChange={(event) => set("websiteUrl", event.target.value)}
            />
            <FieldError message={fieldError("websiteUrl")} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="linkedinUrl">LinkedIn</Label>
            <Input
              id="linkedinUrl"
              type="url"
              placeholder="https://"
              value={draft.linkedinUrl}
              onChange={(event) => set("linkedinUrl", event.target.value)}
            />
            <FieldError message={fieldError("linkedinUrl")} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="githubUrl">GitHub</Label>
            <Input
              id="githubUrl"
              type="url"
              placeholder="https://"
              value={draft.githubUrl}
              onChange={(event) => set("githubUrl", event.target.value)}
            />
            <FieldError message={fieldError("githubUrl")} />
          </div>
        </div>
      </section>

      <div className="flex items-center gap-3">
        <Button type="submit" variant="accent" disabled={saving}>
          {saving ? "Saving…" : "Save profile"}
        </Button>
        <p className="text-xs text-muted-foreground" aria-live="polite">
          {saving ? "Saving your profile…" : null}
        </p>
      </div>
    </form>
  );
}
