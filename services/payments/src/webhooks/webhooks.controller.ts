/**
 * The provider callback route.
 *
 * It is `@Public()` because a payment provider has no session and never will:
 * the signature over the raw body is the entire authentication of this
 * endpoint, which is why `main.ts` keeps the raw bytes and why nothing here
 * touches a parsed body.
 *
 * It answers 200 for anything it understood, including events it deliberately
 * ignored, because a provider retries on any non-2xx. The only non-2xx it ever
 * returns is 401 for a signature that did not verify — and that answer carries
 * no hint as to why.
 */
import { Controller, HttpCode, Post, Req } from '@nestjs/common';
import { Public, badRequest } from '@karmarat/nestshared';
import type { RawBodyRequest } from '@nestjs/common';
import type { Request } from 'express';

import { toProblem } from '../providers/provider-error';
import { WebhooksService } from './webhooks.service';

/**
 * Signature headers, in the order they are tried.
 *
 * Stripe's own header name first, then the generic ones the manual provider and
 * most CLI tooling emit. Reading several costs nothing and means an operator
 * settling a comped account by hand does not have to guess.
 */
const SIGNATURE_HEADERS = ['stripe-signature', 'x-webhook-signature', 'x-signature'] as const;

@Controller('v1/webhooks')
@Public()
export class WebhooksController {
  constructor(private readonly webhooks: WebhooksService) {}

  @Post('payments')
  @HttpCode(200)
  async receive(@Req() request: RawBodyRequest<Request>): Promise<Record<string, unknown>> {
    const rawBody = request.rawBody;
    if (!rawBody || rawBody.length === 0) {
      // Not 401: nothing was presented to verify. A provider that sent an empty
      // body has a configuration problem, and saying so costs no security
      // because no signature was involved either way.
      throw badRequest('A webhook body is required.');
    }

    const signature = readSignature(request);

    let result;
    try {
      result = await this.webhooks.handle(rawBody, signature);
    } catch (error) {
      // WebhookVerificationError becomes a bare 401 with a fixed detail; the
      // real reason is logged, never returned.
      throw toProblem(error);
    }

    // 200 in every one of these cases, including 'failed': the event is
    // recorded and a redelivery would not help, so telling the provider to
    // retry forever would only hide the problem behind noise.
    return { received: true, status: result.status, eventId: result.eventId };
  }
}

function readSignature(request: Request): string {
  for (const name of SIGNATURE_HEADERS) {
    const value = request.headers[name];
    const first = Array.isArray(value) ? value[0] : value;
    if (first) {
      return first;
    }
  }
  return '';
}
