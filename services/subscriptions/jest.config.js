/**
 * Unit tests run with no dependencies at all; the tests that need Postgres
 * check TEST_DATABASE_URL themselves and skip when it is unset, so `pnpm test`
 * is green on a laptop with nothing running and meaningful in CI.
 */
module.exports = {
  preset: 'ts-jest',
  testEnvironment: 'node',
  rootDir: '.',
  roots: ['<rootDir>/src'],
  testRegex: '.*\\.spec\\.ts$',
  moduleFileExtensions: ['ts', 'js', 'json'],
  transform: { '^.+\\.ts$': ['ts-jest', { tsconfig: '<rootDir>/tsconfig.json' }] },
  // Postgres-backed suites open a pool; give them room to tear it down.
  testTimeout: 30_000,
};
