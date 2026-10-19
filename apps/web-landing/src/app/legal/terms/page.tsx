import type { Metadata } from "next";

import { LegalPlaceholder } from "@/components/legal-placeholder";

export const metadata: Metadata = {
  title: "Terms of service",
  description: "Karmarat's terms of service — not yet published.",
  robots: { index: false },
};

export default function TermsPage() {
  return (
    <LegalPlaceholder
      title="Terms of service"
      intro="The sign-up form links here because a real product has to, and because a link to a page that does not exist is worse than a page that admits it does not."
      willCover={[
        "Who may open a company account, and on what basis the platform may review, suspend or close one.",
        "What a subscription buys, how it renews, and what happens to a company's data when it lapses or is cancelled.",
        "The separation between a candidate account and a company account, and what each side may do with the other's data.",
        "Acceptable use — in particular, what a company may and may not ask for in an application form.",
        "Liability, governing law, and how the terms themselves can change.",
      ]}
    />
  );
}
