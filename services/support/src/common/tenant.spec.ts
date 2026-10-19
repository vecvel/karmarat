/**
 * The tenant id check.
 *
 * It exists so a malformed company id becomes a deliberate 403 instead of a
 * Postgres cast error, which would put a type name and the offending literal
 * into a response body.
 */
import { Problem } from '@karmarat/nestshared';

import { assertTenantId, isTenantId } from './tenant';

describe('assertTenantId', () => {
  it('returns a well-formed uuid unchanged', () => {
    const id = '11111111-1111-4111-8111-111111111111';
    expect(assertTenantId(id)).toBe(id);
  });

  it.each([
    ['an empty string', ''],
    ['a bare word', 'acme'],
    ['SQL', "' OR 1=1 --"],
    ['a uuid with a trailing segment', '11111111-1111-4111-8111-111111111111x'],
    ['a truncated uuid', '11111111-1111-4111-8111'],
  ])('refuses %s', (_label, value) => {
    expect(() => assertTenantId(value)).toThrow(Problem);
    expect(isTenantId(value)).toBe(false);
  });

  it('refuses with 403 and says nothing about the value it saw', () => {
    try {
      assertTenantId('not-a-uuid');
      fail('expected a Problem');
    } catch (error) {
      const problem = error as Problem;
      expect(problem.getStatus()).toBe(403);
      expect(JSON.stringify(problem.getResponse())).not.toContain('not-a-uuid');
    }
  });
});
