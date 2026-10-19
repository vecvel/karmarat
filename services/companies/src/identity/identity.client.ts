/**
 * Calls the identity service's internal provisioning API.
 *
 * Identity owns accounts, roles and memberships; this service owns the company
 * record. Registration therefore spans both, and identity is the authority on
 * whether the owner's email is already taken and whether the slug is free in its
 * own projection. It is asked synchronously — a registration that "succeeded"
 * but left the owner unable to sign in is worse than one that failed cleanly.
 */
import { Injectable, Logger } from '@nestjs/common';

import { Problem, type ProblemBody } from '@karmarat/nestshared';

import type { CompaniesConfig } from '../config';

export interface ProvisionCompanyInput {
  companyId: string;
  slug: string;
  name: string;
  ownerEmail: string;
  ownerName: string;
  ownerPassword: string;
  state: string;
}

export interface ProvisionCompanyResult {
  companyId: string;
  slug: string;
  ownerAccountId: string;
  membershipId: string;
  ownerCreated: boolean;
}

/**
 * Statuses that describe the *caller's* request rather than the platform's
 * internals, and may therefore be passed through.
 *
 * An allow-list matters here: identity answers 401 when this service presents a
 * bad internal token, and forwarding that would tell a visitor their password
 * was wrong when in truth a deploy is misconfigured.
 */
const FORWARDABLE_STATUSES = new Set([409, 422]);

@Injectable()
export class IdentityClient {
  private readonly logger = new Logger(IdentityClient.name);
  private readonly baseUrl: string;

  constructor(private readonly config: CompaniesConfig) {
    this.baseUrl = config.identityUrl.replace(/\/+$/, '');
  }

  async provisionCompany(input: ProvisionCompanyInput): Promise<ProvisionCompanyResult> {
    const response = await this.post('/internal/companies', input);

    if (response.ok) {
      return (await response.json()) as ProvisionCompanyResult;
    }

    const problem = await readProblem(response);

    if (FORWARDABLE_STATUSES.has(response.status)) {
      // Re-thrown as our own Problem so the response still carries this
      // service's instance path, and so a future identity field cannot be
      // proxied out unexamined.
      throw new Problem(
        response.status,
        problem?.code ?? 'conflict',
        problem?.title ?? 'Conflict',
        problem?.detail ?? 'The registration could not be completed.',
        problem?.errors,
      );
    }

    this.logger.error(
      `identity rejected provisioning with ${response.status} (${problem?.code ?? 'no code'}) ` +
        `for slug ${input.slug}`,
    );
    throw unavailable();
  }

  private async post(path: string, body: unknown): Promise<Response> {
    try {
      return await fetch(`${this.baseUrl}${path}`, {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          accept: 'application/json',
          'x-internal-token': this.config.internalToken,
        },
        body: JSON.stringify(body),
        // Without a deadline a hung identity holds a registration open until the
        // client gives up, and the compensating delete never runs.
        signal: AbortSignal.timeout(this.config.identityTimeoutMs),
      });
    } catch (error) {
      this.logger.error(`identity call to ${path} failed: ${(error as Error).message}`);
      throw unavailable();
    }
  }
}

function unavailable(): Problem {
  return new Problem(
    503,
    'registration_unavailable',
    'Service Unavailable',
    'Registration is temporarily unavailable. Please try again in a moment.',
  );
}

async function readProblem(response: Response): Promise<ProblemBody | null> {
  try {
    const body = (await response.json()) as ProblemBody;
    return typeof body === 'object' && body !== null ? body : null;
  } catch {
    return null;
  }
}
