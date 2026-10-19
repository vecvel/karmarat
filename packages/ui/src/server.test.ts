import assert from 'node:assert/strict';
import http from 'node:http';
import test from 'node:test';

import { gatewayRequest, isJson, unreachableProblem } from './server.ts';

/**
 * These tests run against a real HTTP server on a real socket.
 *
 * Nothing smaller would prove the thing that matters. The bug this module
 * exists to fix is that Node's `fetch` drops a `Host` header silently, which no
 * unit test of the request-building code could ever catch — the header is
 * present right up until the bytes go out. So the assertion has to be made by
 * something that reads what actually arrived.
 */
function withServer(
  handler: (req: http.IncomingMessage, res: http.ServerResponse) => void,
  run: (origin: string) => Promise<void>,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const server = http.createServer(handler);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (typeof address === 'string' || address === null) {
        server.close();
        reject(new Error('server did not bind a port'));
        return;
      }
      run(`http://127.0.0.1:${address.port}`)
        .then(() => server.close(() => resolve()))
        .catch((error) => server.close(() => reject(error)));
    });
  });
}

test('the portal host arrives as the Host header, not the connection address', async () => {
  let seen = '';

  await withServer(
    (req, res) => {
      seen = req.headers.host ?? '';
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end('{"ok":true}');
    },
    async (origin) => {
      const response = await gatewayRequest({
        gatewayUrl: origin,
        portalHost: 'acme.karmarat.local',
        path: '/api/v1/recruiters',
      });
      assert.equal(response.status, 200);
    },
  );

  // The whole point. `fetch` would have sent `127.0.0.1:<port>` here, the
  // gateway would have resolved the public portal, and every company route
  // would have answered 404.
  assert.equal(seen, 'acme.karmarat.local');
});

test('the path and query reach the upstream intact', async () => {
  let seen = '';

  await withServer(
    (req, res) => {
      seen = req.url ?? '';
      res.writeHead(204);
      res.end();
    },
    async (origin) => {
      // A trailing slash on the configured origin must not produce a double one.
      const response = await gatewayRequest({
        gatewayUrl: `${origin}/`,
        portalHost: 'jobs.karmarat.local',
        path: '/api/v1/public/jobs?limit=20&q=engineer',
      });
      assert.equal(response.status, 204);
    },
  );

  assert.equal(seen, '/api/v1/public/jobs?limit=20&q=engineer');
});

test('trust headers a caller supplies are not forwarded', async () => {
  const received: Record<string, string | string[] | undefined> = {};

  await withServer(
    (req, res) => {
      Object.assign(received, req.headers);
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end('{}');
    },
    async (origin) => {
      await gatewayRequest({
        gatewayUrl: origin,
        portalHost: 'root.karmarat.local',
        path: '/api/v1/admin/companies',
        accessToken: 'token-123',
        headers: {
          // The gateway strips these itself. A portal forwarding them would
          // still be volunteering to look like an attacker.
          'X-Principal-Type': 'platform',
          'X-Company-ID': '00000000-0000-0000-0000-000000000000',
          'X-Permissions': 'platform_companies.read',
          // A header with no trust meaning passes through.
          'X-Request-Source': 'admin-console',
        },
      });
    },
  );

  assert.equal(received['x-principal-type'], undefined);
  assert.equal(received['x-company-id'], undefined);
  assert.equal(received['x-permissions'], undefined);
  assert.equal(received['x-request-source'], 'admin-console');
  assert.equal(received['authorization'], 'Bearer token-123');
});

test('a Headers instance is accepted as well as a plain object', async () => {
  const received: Record<string, string | string[] | undefined> = {};

  await withServer(
    (req, res) => {
      Object.assign(received, req.headers);
      res.writeHead(200);
      res.end();
    },
    async (origin) => {
      const headers = new Headers({ 'X-Request-Source': 'company-portal', Cookie: 'rb_refresh=abc' });
      await gatewayRequest({
        gatewayUrl: origin,
        portalHost: 'acme.karmarat.local',
        path: '/api/v1/company/profile',
        headers,
      });
    },
  );

  assert.equal(received['x-request-source'], 'company-portal');
  // The session cookie is this portal's own; upstream authenticates on the
  // bearer token and has no business seeing it.
  assert.equal(received['cookie'], undefined);
});

test('a body is sent with its content type and length', async () => {
  let body = '';
  const received: Record<string, string | string[] | undefined> = {};

  await withServer(
    (req, res) => {
      Object.assign(received, req.headers);
      req.on('data', (chunk) => (body += chunk));
      req.on('end', () => {
        res.writeHead(201, { 'content-type': 'application/json' });
        res.end('{"id":"role_1"}');
      });
    },
    async (origin) => {
      const response = await gatewayRequest({
        gatewayUrl: origin,
        portalHost: 'acme.karmarat.local',
        path: '/api/v1/company-roles',
        method: 'POST',
        body: JSON.stringify({ name: 'Night Sourcer' }),
      });
      assert.equal(response.status, 201);
      assert.equal(response.body, '{"id":"role_1"}');
      assert.ok(isJson(response.headers));
    },
  );

  assert.equal(body, '{"name":"Night Sourcer"}');
  assert.equal(received['content-type'], 'application/json');
  assert.equal(received['content-length'], '24');
});

test('a non-2xx is returned rather than thrown', async () => {
  await withServer(
    (_req, res) => {
      res.writeHead(403, { 'content-type': 'application/problem+json' });
      res.end('{"status":403,"code":"forbidden"}');
    },
    async (origin) => {
      // Callers differ in what a refusal means: a proxy streams it to the
      // browser, an auth handler turns it into a cookie decision. Throwing here
      // would force every one of them to catch to read a status.
      const response = await gatewayRequest({
        gatewayUrl: origin,
        portalHost: 'acme.karmarat.local',
        path: '/api/v1/recruiters',
      });
      assert.equal(response.status, 403);
      assert.match(response.body, /forbidden/);
    },
  );
});

test('hop-by-hop response headers are not passed on', async () => {
  await withServer(
    (_req, res) => {
      res.writeHead(200, {
        'content-type': 'application/json',
        Connection: 'keep-alive',
        'Set-Cookie': 'upstream=1',
      });
      res.end('{}');
    },
    async (origin) => {
      const response = await gatewayRequest({
        gatewayUrl: origin,
        portalHost: 'acme.karmarat.local',
        path: '/api/v1/company/profile',
      });
      assert.equal(response.headers.get('connection'), null);
      // A cookie set by upstream is scoped to the gateway's host; on a portal's
      // own origin it is meaningless at best.
      assert.equal(response.headers.get('set-cookie'), null);
      assert.equal(response.headers.get('content-type'), 'application/json');
    },
  );
});

test('an unreachable gateway rejects rather than resolving', async () => {
  await assert.rejects(() =>
    gatewayRequest({
      // Port 1 is never listening; the connection is refused immediately.
      gatewayUrl: 'http://127.0.0.1:1',
      portalHost: 'acme.karmarat.local',
      path: '/api/v1/company/profile',
      timeoutMs: 2_000,
    }),
  );
});

test('unreachableProblem is a valid problem document', () => {
  const problem = unreachableProblem('/api/v1/company/profile');
  const body = JSON.parse(problem.body) as Record<string, unknown>;

  assert.equal(problem.status, 502);
  assert.equal(body.status, 502);
  assert.equal(body.code, 'gateway_unreachable');
  assert.equal(body.instance, '/api/v1/company/profile');
  assert.ok(isJson(problem.headers));
});

test('a connection dropped mid-body rejects instead of hanging', async () => {
  // The failure this guards against does not look like a failure. Without an
  // 'error'/'aborted' handler on the response stream, 'end' simply never fires
  // and the promise never settles — a server render awaiting it hangs until the
  // platform's own timeout, with nothing in the log to say why.
  await withServer(
    (_req, res) => {
      res.writeHead(200, { 'content-type': 'application/json', 'content-length': '4096' });
      res.write('{"partial":');
      // Destroying the socket mid-body is what a restarting upstream does.
      res.socket?.destroy();
    },
    async (origin) => {
      await assert.rejects(() =>
        gatewayRequest({
          gatewayUrl: origin,
          portalHost: 'acme.karmarat.local',
          path: '/api/v1/recruiters',
          timeoutMs: 3_000,
        }),
      );
    },
  );
})

test('a slow trickle is bounded by the deadline, not reset by it', async () => {
  // `request.setTimeout` measures inactivity, so an upstream that sends a byte
  // just often enough resets it forever. The call has to end anyway.
  const timers = []
  await withServer(
    (_req, res) => {
      res.writeHead(200, { 'content-type': 'application/json', 'content-length': '9999' });
      res.write('{')
      // Well inside the 600ms deadline, so inactivity alone would never fire.
      timers.push(setInterval(() => res.write(' '), 50))
    },
    async (origin) => {
      const started = process.hrtime.bigint()
      await assert.rejects(() =>
        gatewayRequest({
          gatewayUrl: origin,
          portalHost: 'acme.karmarat.local',
          path: '/api/v1/recruiters',
          timeoutMs: 600,
        }),
      );
      const elapsedMs = Number(process.hrtime.bigint() - started) / 1e6
      assert.ok(elapsedMs < 3_000, `expected the deadline to end it promptly, took ${elapsedMs}ms`)
    },
  );
  timers.forEach((t) => clearInterval(t))
})
