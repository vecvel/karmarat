SHELL := /bin/bash
.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: setup
setup: keys env infra ## First-time setup: keys, env file, infrastructure
	@echo "Setup complete. Next: make migrate"

.PHONY: env
env: ## Create .env from the example when missing
	@test -f .env || (cp .env.example .env && echo "Created .env from .env.example")

.PHONY: keys
keys: ## Generate the RS256 keypair used to sign access tokens
	@mkdir -p deploy/keys
	@test -f deploy/keys/jwt-private.pem || ( \
		openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 \
			-out deploy/keys/jwt-private.pem 2>/dev/null && \
		openssl rsa -pubout -in deploy/keys/jwt-private.pem \
			-out deploy/keys/jwt-public.pem 2>/dev/null && \
		chmod 600 deploy/keys/jwt-private.pem && \
		echo "Generated deploy/keys/jwt-{private,public}.pem" )

.PHONY: infra
infra: ## Start Postgres, Redis, NATS, MinIO, Mailpit, Jaeger
	docker compose -f deploy/docker-compose.yml up -d

.PHONY: infra-down
infra-down: ## Stop infrastructure
	docker compose -f deploy/docker-compose.yml down

.PHONY: infra-reset
infra-reset: ## Destroy infrastructure volumes and start clean
	docker compose -f deploy/docker-compose.yml down -v
	docker compose -f deploy/docker-compose.yml up -d

.PHONY: db
db: ## Create any service database missing from a running Postgres
	@# The init script in deploy/postgres only runs on a FRESH volume, so a
	@# service added later has no database and fails to start with a connection
	@# error that says nothing about the cause.
	@for db in $$(grep -oE 'SERVICE_DATABASES: .*' deploy/docker-compose.yml | cut -d' ' -f2 | tr ',' ' '); do \
		docker compose -f deploy/docker-compose.yml exec -T postgres \
			createdb -U $${POSTGRES_USER:-karmarat} "$$db" 2>/dev/null \
			&& echo "created $$db" || true; \
	done
	@echo "service databases ready"

.PHONY: migrate
migrate: db ## Apply database migrations for every Go service
	@# Discovered rather than listed: a service whose migrations are never run
	@# starts against an empty schema and fails on its first query.
	@for dir in services/*/cmd/migrate; do \
		svc=$$(echo $$dir | cut -d/ -f2); \
		echo "migrating $$svc"; \
		go run ./$$dir || exit 1; \
	done

.PHONY: db-clean
db-clean: ## Drop all service databases, recreate, and apply fresh migrations
	./scripts/db-clean.sh

.PHONY: db-clean-seed
db-clean-seed: ## Drop databases, recreate, migrate, and seed UAT demo data
	./scripts/db-clean.sh --seed

.PHONY: db-pull
db-pull: ## Pull remote database into local (e.g. make db-pull FROM=user@host SANITIZE=1)
	@if [ -z "$(FROM)" ] && [ -z "$(FILE)" ]; then \
		echo "Usage: make db-pull FROM=user@staging.karmarat.com [SANITIZE=1]"; \
		echo "   or: make db-pull FILE=/path/to/backup.sql.gz [SANITIZE=1]"; \
		exit 1; \
	fi
	./scripts/db-pull.sh $(if $(FROM),--from $(FROM)) $(if $(FILE),--file $(FILE)) $(if $(filter 1 true,$(SANITIZE)),--sanitize)

.PHONY: db-push
db-push: ## Push local database snapshot to remote server (e.g. make db-push TO=user@host)
	@if [ -z "$(TO)" ]; then \
		echo "Usage: make db-push TO=user@staging.karmarat.com [FILE=/path/to/backup.sql.gz]"; \
		exit 1; \
	fi
	./scripts/db-push.sh --to $(TO) $(if $(FILE),--file $(FILE))

.PHONY: build
build: ## Build all Go services
	go build ./services/... ./packages/...

.PHONY: test
test: ## Run all Go tests
	go test ./services/... ./packages/... -race -count=1

.PHONY: clean
clean: ## Remove compiled binaries, local runtime logs, and test artifacts
	rm -rf bin .run/logs/* *.test *.out

.PHONY: tidy
tidy: ## Tidy the Go module
	go mod tidy

.PHONY: run-gateway
run-gateway: ## Run the API gateway
	go run ./services/gateway/cmd/server

.PHONY: run-identity
run-identity: ## Run the identity service
	go run ./services/identity/cmd/server

.PHONY: dev
dev: ## Start infrastructure, every service, and the company portal
	./scripts/dev.sh up

.PHONY: dev-down
dev-down: ## Stop the services and the company portal
	./scripts/dev.sh down

.PHONY: status
status: ## Show what is running
	./scripts/dev.sh status

.PHONY: smoke
smoke: ## Assert the security boundaries against a running stack
	./scripts/smoke.sh

.PHONY: smoke-product
smoke-product: ## Walk the whole product flow against a running stack
	./scripts/smoke-product.sh

.PHONY: smoke-all
smoke-all: smoke smoke-product ## Both suites

COMPOSE := docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.services.yml
COMPOSE_PROD := docker compose -f deploy/docker-compose.prod.yml

.PHONY: images
images: ## Build every service image, to prove the deployment path still works
	@# Worth its own target because an unbuildable image is invisible from the
	@# host: `dev.sh up` runs the services as processes and never touches a
	@# Dockerfile, so all five NestJS images were broken by the same missing
	@# pnpm flag for as long as nobody ran a containerised build.
	$(COMPOSE) build

.PHONY: prod-build
prod-build: ## Build all production images (frontends and backend services)
	$(COMPOSE_PROD) build

.PHONY: prod-up
prod-up: keys ## Start the full production stack with Caddy ingress
	$(COMPOSE_PROD) up -d

.PHONY: prod-down
prod-down: ## Stop the production stack
	$(COMPOSE_PROD) down

.PHONY: prod-logs
prod-logs: ## Follow logs from the production stack
	$(COMPOSE_PROD) logs -f

.PHONY: backup
backup: ## Create a snapshot backup of PostgreSQL and MinIO
	./scripts/backup.sh

.PHONY: env-check
env-check: ## Validate environment configuration invariants
	./scripts/check-env.sh .env

.PHONY: seed-uat
seed-uat: ## Seed UAT/Staging with realistic demo tenants and candidates
	./scripts/seed-uat.sh

.PHONY: stack-up
stack-up: keys ## Run everything in containers, services included
	$(COMPOSE) up -d --build

.PHONY: stack-down
stack-down: ## Stop the containerised stack
	$(COMPOSE) down

.PHONY: stack-logs
stack-logs: ## Follow logs from the containerised stack
	$(COMPOSE) logs -f

.PHONY: gen
gen: ## Regenerate code derived from another language's source
	./scripts/gen-reserved-slugs.sh

.PHONY: rebrand
rebrand: ## Rebrand platform (e.g. make rebrand OLD=oldname NEW=newname [DRY=1] [FORCE=1])
	@if [ -z "$(OLD)" ] || [ -z "$(NEW)" ]; then \
		echo "Usage: make rebrand OLD=<OldName> NEW=<NewName> [DRY=1] [FORCE=1]"; \
		echo "Example: make rebrand OLD=oldname NEW=newname DRY=1"; \
		exit 1; \
	fi
	./scripts/rebrand.sh --old "$(OLD)" --new "$(NEW)" $(if $(filter 1 true,$(DRY)),--dry-run) $(if $(filter 1 true,$(FORCE)),--force)

.PHONY: verify
verify: ## Everything CI runs, locally
	go build ./services/... ./packages/...
	go vet ./services/... ./packages/...
	@test -z "$$(gofmt -l services packages)" || (echo "gofmt needed:"; gofmt -l services packages; exit 1)
	go test ./services/... ./packages/... -race -count=1
	./scripts/gen-reserved-slugs.sh --check
	node scripts/check-permission-keys.mjs
