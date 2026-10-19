/**
 * Errors a provider may raise, and their translation into problem documents.
 *
 * A provider SDK's error message routinely carries a request id, an API key
 * prefix or a stack frame from inside the SDK. None of that belongs in a
 * response body, so every provider failure is wrapped here with an explicit
 * `safeDetail` — the only part a client ever sees. The original stays on the
 * error for the log.
 */
import { Problem, badRequest, unauthorized } from '@karmarat/nestshared';

export class ProviderError extends Error {
  constructor(
    message: string,
    readonly safeDetail: string,
    /** Whether the caller may usefully try again. */
    readonly retryable = false,
  ) {
    super(message);
    this.name = 'ProviderError';
  }
}

/**
 * The provider genuinely cannot answer this question.
 *
 * Distinct from a failure: the manual provider has no remote record of a
 * payment, so `getPayment` is unanswerable rather than broken, and a caller may
 * reasonably carry on with what it knows locally.
 */
export class ProviderUnsupportedError extends ProviderError {
  constructor(operation: string) {
    super(
      `provider does not support ${operation}`,
      'This operation is not available for the payment provider in use.',
    );
    this.name = 'ProviderUnsupportedError';
  }
}

/** A webhook whose signature did not verify. Always a 401 — never a hint why. */
export class WebhookVerificationError extends ProviderError {
  constructor(message: string) {
    super(message, 'The webhook signature could not be verified.');
    this.name = 'WebhookVerificationError';
  }
}

/**
 * Maps a provider failure onto the platform's error shape.
 *
 * A provider being down is a 503 and not a 500: the request was well formed and
 * retrying it later is the correct client behaviour.
 */
export function toProblem(error: unknown): Problem {
  if (error instanceof WebhookVerificationError) {
    return unauthorized(error.safeDetail);
  }
  if (error instanceof ProviderUnsupportedError) {
    return badRequest(error.safeDetail);
  }
  if (error instanceof ProviderError) {
    return error.retryable
      ? new Problem(503, 'provider_unavailable', 'Service Unavailable', error.safeDetail)
      : new Problem(502, 'provider_error', 'Bad Gateway', error.safeDetail);
  }
  return new Problem(502, 'provider_error', 'Bad Gateway', 'The payment provider could not be reached.');
}
