.PHONY: test-backend test-agent test-web test-evals test up down

test-backend:
	cd backend && go test ./... && go vet ./...

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
