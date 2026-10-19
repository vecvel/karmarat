import type { Metadata } from "next";

import { LegalPlaceholder } from "@/components/legal-placeholder";

export const metadata: Metadata = {
  title: "Privacy notice",
  description: "Karmarat's privacy notice — not yet published.",
  robots: { index: false },
};

export default function PrivacyPage() {
  return (
    <LegalPlaceholder
      title="Privacy notice"
      intro="This product handles résumés, application histories and messages between people looking for work and people deciding whether to hire them. That deserves a real notice written by someone qualified to write one, which does not exist yet."
      willCover={[
        "What a candidate profile contains, who can see it, and how discoverability changes that.",
        "What a company sees about an applicant, and for how long it keeps it.",
        "Where résumés and uploaded files are stored, and how access to them is controlled.",
        "The lawful basis for each kind of processing, and the rights a candidate has over their own record.",
        "Which third parties are involved — payment processing and email delivery in particular — and what they receive.",
        "Retention periods, deletion, and what survives an account being closed.",
      ]}
    />
  );
}
