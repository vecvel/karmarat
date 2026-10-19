/**
 * The ticket state machine.
 *
 * Table-driven, because the interesting property is not any single transition
 * but that both sides of the desk agree on every one of them: the company
 * controller and the platform controller call into the same two functions, and
 * a divergence here would land a ticket in a state neither queue selects for.
 */
import { Problem } from '@karmarat/nestshared';

import {
  assertClosable,
  isClosed,
  isTicketCategory,
  isTicketPriority,
  isTicketState,
  stateAfterCompanyReply,
  stateAfterPlatformReply,
  TICKET_STATES,
  type TicketState,
} from './domain';

describe('stateAfterCompanyReply', () => {
  const cases: Array<{ from: TicketState; to: TicketState }> = [
    { from: 'open', to: 'awaiting_support' },
    { from: 'awaiting_customer', to: 'awaiting_support' },
    { from: 'awaiting_support', to: 'awaiting_support' },
    // A customer writing back is a customer disagreeing that it is resolved.
    { from: 'resolved', to: 'awaiting_support' },
  ];

  it.each(cases)('moves $from to $to', ({ from, to }) => {
    expect(stateAfterCompanyReply(from)).toBe(to);
  });

  it('refuses a closed ticket with a 409 rather than reopening it', () => {
    expect(() => stateAfterCompanyReply('closed')).toThrow(Problem);
    try {
      stateAfterCompanyReply('closed');
    } catch (error) {
      expect((error as Problem).getStatus()).toBe(409);
    }
  });
});

describe('stateAfterPlatformReply', () => {
  const cases: Array<{ from: TicketState; internal: boolean; to: TicketState }> = [
    { from: 'open', internal: false, to: 'awaiting_customer' },
    { from: 'awaiting_support', internal: false, to: 'awaiting_customer' },
    { from: 'resolved', internal: false, to: 'awaiting_customer' },
    // An internal note must not move the ticket. Moving it would tell the
    // customer, through the state alone, that something happened they cannot
    // see — and would drop the ticket out of the desk's own reply queue.
    { from: 'open', internal: true, to: 'open' },
    { from: 'awaiting_support', internal: true, to: 'awaiting_support' },
    { from: 'awaiting_customer', internal: true, to: 'awaiting_customer' },
    { from: 'resolved', internal: true, to: 'resolved' },
  ];

  it.each(cases)('moves $from to $to when internal is $internal', ({ from, internal, to }) => {
    expect(stateAfterPlatformReply(from, internal)).toBe(to);
  });

  it.each([true, false])('refuses a closed ticket when internal is %s', (internal) => {
    expect(() => stateAfterPlatformReply('closed', internal)).toThrow(Problem);
  });
});

describe('assertClosable', () => {
  it.each(TICKET_STATES.filter((state) => state !== 'closed'))('accepts %s', (state) => {
    expect(() => assertClosable(state)).not.toThrow();
  });

  it('rejects a second close, because that is the same mutation applied twice', () => {
    expect(() => assertClosable('closed')).toThrow(Problem);
  });
});

describe('vocabulary guards', () => {
  it.each([
    ['open', true],
    ['closed', true],
    ['OPEN', false],
    ['deleted', false],
    ['', false],
  ])('isTicketState(%s) is %s', (value, expected) => {
    expect(isTicketState(value as string)).toBe(expected);
  });

  it.each([
    ['urgent', true],
    ['normal', true],
    ['critical', false],
  ])('isTicketPriority(%s) is %s', (value, expected) => {
    expect(isTicketPriority(value as string)).toBe(expected);
  });

  it.each([
    ['billing', true],
    ['feature_request', true],
    ['refund', false],
  ])('isTicketCategory(%s) is %s', (value, expected) => {
    expect(isTicketCategory(value as string)).toBe(expected);
  });

  it('treats only closed as terminal', () => {
    for (const state of TICKET_STATES) {
      expect(isClosed(state)).toBe(state === 'closed');
    }
  });
});
