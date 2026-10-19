import "server-only";

/**
 * Per-tenant SMTP settings.
 *
 * These were stored in the company portal's own database. On the platform,
 * notifications owns email fan-out and exposes no per-tenant SMTP resource, so
 * the type and its defaults survive to keep the settings screen rendering while
 * the read and write paths report the gap honestly.
 *
 * Listed in lib/gateway/unavailable.ts as "email-settings".
 */

export interface SmtpConfig {
  host: string;
  port: number;
  secure: boolean;
  user: string;
  pass: string;
  fromName: string;
  fromEmail: string;
  replyTo?: string;
  signature?: string;
  autoSendApplicationConfirmation?: boolean;
  autoSendInterviewInvite?: boolean;
  autoSendOfferNotice?: boolean;
  isConfigured?: boolean;
  lastTestedAt?: string;
  lastTestStatus?: "success" | "error";
  lastTestMessage?: string;
}

export const DEFAULT_SMTP_CONFIG: SmtpConfig = {
  host: "smtp.resend.com",
  port: 587,
  secure: false,
  user: "resend",
  pass: "",
  fromName: "Karmarat Talent Team",
  fromEmail: "talent@karmarat.com",
  replyTo: "recruiting@karmarat.com",
  signature: "--\nKarmarat Talent Team\nhttps://karmarat.com",
  autoSendApplicationConfirmation: true,
  autoSendInterviewInvite: true,
  autoSendOfferNotice: true,
  isConfigured: false,
};

/**
 * Internal SMTP credential read.
 *
 * Not a server action: the password must only ever reach the mail transport, so
 * this is callable from server code but never from the browser. The settings
 * screen goes through `getSmtpConfig`, which is permission-guarded and masks the
 * password for anyone who cannot edit it.
 */
export async function readSmtpConfig(_orgId?: string): Promise<SmtpConfig> {
  return { ...DEFAULT_SMTP_CONFIG, isConfigured: false };
}

export async function writeSmtpConfig(
  _config: Partial<SmtpConfig>,
  _orgId?: string,
): Promise<SmtpConfig> {
  return { ...DEFAULT_SMTP_CONFIG, isConfigured: false };
}
