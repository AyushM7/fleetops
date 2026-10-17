.PHONY: test-backend test-integration test-agent test-web test-evals test up down

test-backend:
	cd backend && go test ./... && go vet ./...

test-integration:
	cd backend && go test -race -tags fleetops_test ./db/... ./fleet/... ./graphql/... -run Integration -count=1 -v

test-agent:
	cd agent && npm test && npm run typecheck

test-web:
	cd web && npm test && npm run typecheck

test-evals:
	cd evals && npm test && npm run typecheck

test: test-backend test-agent test-web test-evals

up:
	@test -f .env || { echo "Missing .env: copy .env.example to .env and replace database placeholders privately." >&2; exit 1; }
	docker compose up -d --wait

down:
	docker compose down
