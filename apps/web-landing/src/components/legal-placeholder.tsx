import Link from "next/link";
import { AlertTriangle } from "lucide-react";

import { Container } from "@/components/section";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";

/**
 * A legal page with no legal text in it.
 *
 * Writing plausible-looking terms would be worse than an empty page: a visitor
 * would reasonably believe they had been shown the agreement they are being
 * asked to accept. The honest thing is to say that the document does not exist
 * yet and to be specific about what it will have to cover.
 */
export function LegalPlaceholder({
  title,
  intro,
  willCover,
}: {
  title: string;
  intro: string;
  willCover: string[];
}) {
  return (
    <Container className="max-w-2xl py-12 sm:py-16">
      <h1 className="text-3xl">{title}</h1>

      <Alert className="mt-6 border-warning/40 bg-warning/5">
        <AlertTriangle aria-hidden="true" className="size-4 text-warning" />
        <AlertTitle>This is a placeholder, not a legal document</AlertTitle>
        <AlertDescription>
          No {title.toLowerCase()} has been published for Karmarat yet. Nothing on
          this page is binding on anyone, and it has not been reviewed by a lawyer.
        </AlertDescription>
      </Alert>

      <p className="mt-8 leading-relaxed text-muted-foreground">{intro}</p>

      <h2 className="mt-10 text-lg">What the published version will have to cover</h2>
      <ul className="mt-4 flex list-disc flex-col gap-2 pl-5 text-sm leading-relaxed text-muted-foreground">
        {willCover.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>

      <p className="mt-10 text-sm text-muted-foreground">
        Questions in the meantime? Raise them through the support desk in your company
        portal, or before you have one, on the{" "}
        <Link href="/signup/company" className="underline underline-offset-4">
          sign-up page
        </Link>
        .
      </p>
    </Container>
  );
}
