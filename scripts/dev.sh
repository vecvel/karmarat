#!/usr/bin/env bash
#
# Run the whole platform locally: infrastructure, Go services, and the web app.
#
#   scripts/dev.sh up      start everything (default)
#   scripts/dev.sh down    stop the Go services and the web app
#   scripts/dev.sh status  show what is running
#   scripts/dev.sh logs    tail every service log
#
# Services are compiled to bin/ and run as real processes rather than through
# `go run`, so a PID file is enough to stop exactly the process we started. With
# `go run` the binary lives in a temp directory under a generated name, which is
# why a pattern-matched kill could miss it and leave a stale listener holding the
# port — the next start then fails to bind and exits, while the health check
# still answers from the old process.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_DIR="$ROOT/.run"
LOG_DIR="$RUN_DIR/logs"

cd "$ROOT"
mkdir -p "$RUN_DIR" "$LOG_DIR" bin

# macOS ships bash 3.2, which has no associative arrays, so the port table is a
# function. Ports match the *_URL defaults in .env.example; a service added there
# needs a line here too.
WEB_PORT=3000

port_of() {
  case "$1" in
    gateway)       echo 8080 ;;
    identity)      echo 8081 ;;
    companies)     echo 8082 ;;
    subscriptions) echo 8083 ;;
    payments)      echo 8084 ;;
    jobs)          echo 8085 ;;
    applications)  echo 8086 ;;
    candidates)    echo 8087 ;;
    messaging)     echo 8088 ;;
    notifications) echo 8089 ;;
    support)       echo 8090 ;;
    admin)         echo 8091 ;;
    interviews)    echo 8092 ;;
    offers)        echo 8093 ;;
    audit)         echo 8094 ;;
    web)           echo "$WEB_PORT" ;;
    *)             die "no port known for service '$1'" ;;
  esac
}

# Discover Go services rather than hard-coding them, so a service that lands in
# the tree is picked up without editing this script. The gateway starts LAST:
# it proxies to the others, and starting it first would briefly answer requests
# with 502s.
discover_go_services() {
  local dir name others=""
  for dir in services/*/; do
    name=$(basename "$dir")
    [[ -d "$dir/cmd/server" ]] || continue
    [[ "$name" == "gateway" ]] && continue
    others="$others $name"
  done
  # shellcheck disable=SC2086
  echo $others gateway
}

# Node services are detected by a package.json with a start:prod script.
discover_node_services() {
  local dir name found=""
  for dir in services/*/; do
    name=$(basename "$dir")
    [[ -f "$dir/package.json" ]] || continue
    grep -q '"start:prod"' "$dir/package.json" 2>/dev/null || continue
    found="$found $name"
  done
  # shellcheck disable=SC2086
  echo $found
}

c_red=$'\033[31m'; c_grn=$'\033[32m'; c_ylw=$'\033[33m'; c_dim=$'\033[2m'; c_off=$'\033[0m'
info() { printf '%s\n' "$*"; }
ok()   { printf '%s✓%s %s\n' "$c_grn" "$c_off" "$*"; }
warn() { printf '%s!%s %s\n' "$c_ylw" "$c_off" "$*"; }
die()  { printf '%s✗%s %s\n' "$c_red" "$c_off" "$*" >&2; exit 1; }

load_env() {
  [[ -f .env ]] || die ".env is missing — run 'make env' first"
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
}

# Returns the listening pid, or nothing. Must not fail: `set -o pipefail` would
# otherwise turn "no listener" (lsof exit 1) into a script-ending error.
port_pid() { lsof -nP -iTCP:"$1" -sTCP:LISTEN -t 2>/dev/null | head -1 || true; }

# Wait for a readiness endpoint rather than sleeping a guessed number of seconds.
wait_ready() {
  local name=$1 port=$2 deadline=$((SECONDS + 45))
  while (( SECONDS < deadline )); do
    if [[ "$(curl -fsS -o /dev/null -w '%{http_code}' "http://localhost:$port/readyz" 2>/dev/null)" == "200" ]]; then
      return 0
    fi
    # Fail fast if the process died instead of waiting out the whole deadline.
    if [[ -f "$RUN_DIR/$name.pid" ]] && ! kill -0 "$(cat "$RUN_DIR/$name.pid")" 2>/dev/null; then
      warn "$name exited during startup — last lines:"
      tail -15 "$LOG_DIR/$name.log" >&2
      return 1
    fi
    sleep 0.5
  done
  warn "$name did not become ready within 45s — last lines:"
  tail -15 "$LOG_DIR/$name.log" >&2
  return 1
}

stop_one() {
  # Two statements on purpose: in `local a=$1 b=$a`, bash expands $a from the
  # caller's scope before this frame's assignment lands, so a one-liner here
  # silently resolves the wrong pid file.
  local name=$1
  local pidfile="$RUN_DIR/$name.pid"
  [[ -f "$pidfile" ]] || return 0
  local pid; pid=$(cat "$pidfile")
  if kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null || true
    # Graceful shutdown drains in-flight requests; give it room before forcing.
    local deadline=$((SECONDS + 25))
    while (( SECONDS < deadline )) && kill -0 "$pid" 2>/dev/null; do sleep 0.5; done
    if kill -0 "$pid" 2>/dev/null; then
      warn "$name ignored SIGTERM; sending SIGKILL"
      kill -KILL "$pid" 2>/dev/null || true
    fi
    ok "stopped $name (pid $pid)"
  fi
  rm -f "$pidfile"
}

cmd_down() {
  local name port pid
  for name in $(discover_go_services) $(discover_node_services) web; do stop_one "$name"; done

  # Then sweep our own ports. A pid file can go stale — a crash, a kill -9, or a
  # process started by hand outside this script — and `up` deliberately skips a
  # port it does not own rather than aborting. Without this sweep that skip
  # silently leaves an OLD BINARY serving while a fresh `up` reports success,
  # which is a genuinely confusing way to lose an afternoon.
  for name in $(discover_go_services) $(discover_node_services) web; do
    port=$(port_of "$name")
    pid=$(port_pid "$port")
    [[ -n "$pid" ]] || continue
    warn "$name left a listener on :$port (pid $pid) with no pid file — stopping it"
    kill -TERM "$pid" 2>/dev/null || true
    local deadline=$((SECONDS + 15))
    while (( SECONDS < deadline )) && kill -0 "$pid" 2>/dev/null; do sleep 0.5; done
    kill -0 "$pid" 2>/dev/null && kill -KILL "$pid" 2>/dev/null || true
  done

  ok "all local processes stopped (infrastructure left running)"
}

cmd_status() {
  local name port pid
  printf '%-14s %-7s %-9s %s\n' SERVICE PORT STATE PID
  for name in $(discover_go_services) $(discover_node_services) web; do
    port=$(port_of "$name")
    pid=$(port_pid "$port")
    printf '%-14s %-7s %-9s %s\n' "$name" "$port" \
      "$([[ -n $pid ]] && echo running || echo stopped)" "${pid:-—}"
  done
  printf '\n%sinfrastructure%s\n' "$c_dim" "$c_off"
  docker compose -f deploy/docker-compose.yml ps --format '  {{.Service}}\t{{.Status}}' 2>/dev/null \
    || warn "docker is not reachable"
}

cmd_logs() { tail -f "$LOG_DIR"/*.log; }

cmd_up() {
  load_env

  [[ -f deploy/keys/jwt-private.pem ]] || die "signing keys are missing — run 'make keys'"

  # A stopped Docker daemon is the single most common reason everything below
  # fails, and the resulting errors — connection refused from five services at
  # once — point everywhere except the cause. Say it plainly instead.
  if ! docker info >/dev/null 2>&1; then
    die "the Docker daemon is not running. Start it (OrbStack: 'orb start', Docker Desktop: open it) and try again."
  fi

  info "starting infrastructure..."
  docker compose -f deploy/docker-compose.yml up -d >/dev/null
  ok "infrastructure up"

  local go_services node_services
  go_services=$(discover_go_services)
  node_services=$(discover_node_services)

  info "building services:$go_services"
  local name buildable=""
  for name in $go_services; do
    if go build -o "bin/$name" "./services/$name/cmd/server" 2>"$LOG_DIR/$name.build.log"; then
      buildable="$buildable $name"
    else
      # A service still being written should not keep the rest of the platform
      # down. Skip it loudly instead: the gateway will answer 502 for its routes,
      # which is exactly what it would do if the service were deployed and dead.
      warn "$name does not compile — skipping it (see $LOG_DIR/$name.build.log)"
      head -3 "$LOG_DIR/$name.build.log" >&2
    fi
  done
  go_services="$buildable"
  [[ -n "$go_services" ]] || die "no service compiles; nothing to run"
  ok "services built:$go_services"

  local port squatter
  for name in $go_services; do
    port=$(port_of "$name")
    stop_one "$name"
    # A listener we did not start means something else owns the port; binding
    # would fail and leave the old process answering health checks.
    squatter=$(port_pid "$port")
    if [[ -n "$squatter" ]]; then
      warn "port $port is held by pid $squatter, which this script did not start — skipping $name"
      continue
    fi
    nohup "./bin/$name" >"$LOG_DIR/$name.log" 2>&1 &
    echo $! >"$RUN_DIR/$name.pid"
    wait_ready "$name" "$port" || die "$name failed to start — see $LOG_DIR/$name.log"
    ok "$name ready on :$port"
  done

  for name in $node_services; do
    port=$(port_of "$name")
    stop_one "$name"
    # start:prod runs the compiled output. Without it the service cannot start,
    # and waiting out the readiness deadline to discover that costs 45s per
    # unbuilt service — which, with several in flight, is most of a `make dev`.
    if [[ ! -d "services/$name/dist" ]]; then
      warn "$name is not built — skipping it (run: pnpm --filter @karmarat/$name build)"
      continue
    fi
    squatter=$(port_pid "$port")
    if [[ -n "$squatter" ]]; then
      warn "port $port is held by pid $squatter, which this script did not start — skipping $name"
      continue
    fi
    # `node dist/main`, not `npm run start:prod`. npm forks the real server as a
    # child, so the pid we record is the wrapper: stopping it leaves the server
    # alive and still holding the port, and the next start then refuses to bind.
    # The same reason the Go services run compiled binaries rather than `go run`.
    ( cd "services/$name" && nohup node dist/main >"$LOG_DIR/$name.log" 2>&1 & echo $! >"$RUN_DIR/$name.pid" )
    # Same tolerance as the Go services: one unfinished service must not take the
    # platform down with it.
    if wait_ready "$name" "$port"; then
      ok "$name ready on :$port"
    else
      warn "$name did not start — skipping it (see $LOG_DIR/$name.log)"
      stop_one "$name"
    fi
  done

  if [[ "${WITH_WEB:-1}" == "1" ]]; then
    stop_one web
    if [[ -n "$(port_pid "$WEB_PORT")" ]]; then
      warn "port $WEB_PORT is already in use; skipping the web app"
    else
      ( cd apps/web-company && nohup npm run dev >"$LOG_DIR/web.log" 2>&1 & echo $! >"$RUN_DIR/web.pid" )
      ok "web app starting on :$WEB_PORT (first compile takes a few seconds)"
    fi
  fi

  echo
  ok "platform is up"
  cat <<EOF

  gateway    http://localhost:8080
  identity   http://localhost:8081
  web        http://localhost:3000
  mailpit    http://localhost:8025
  minio      http://localhost:9001
  jaeger     http://localhost:16686

  logs    scripts/dev.sh logs
  stop    scripts/dev.sh down
EOF
}

case "${1:-up}" in
  up)     cmd_up ;;
  down)   cmd_down ;;
  status) cmd_status ;;
  logs)   cmd_logs ;;
  *)      die "unknown command '${1}' (expected: up, down, status, logs)" ;;
esac
