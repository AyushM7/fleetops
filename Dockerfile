ARG NODE_IMAGE=node:24.21.0-bookworm
ARG GO_IMAGE=golang:1.27.1-bookworm
FROM ${NODE_IMAGE} AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM ${GO_IMAGE} AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -p 1 -trimpath -o /fleetops ./cmd/server
RUN CGO_ENABLED=0 go build -p 1 -trimpath -o /healthcheck ./cmd/healthcheck

FROM ${NODE_IMAGE} AS agent
WORKDIR /src/agent
COPY agent/package.json agent/package-lock.json ./
RUN npm ci
COPY agent/ ./
RUN npm run build && npm prune --omit=dev

FROM ${NODE_IMAGE} AS operations
COPY --from=backend /fleetops /fleetops
COPY --from=backend /healthcheck /healthcheck
COPY --from=web /src/web/dist /web
COPY --from=agent /src/agent/package.json /agent/package.json
COPY --from=agent /src/agent/dist /agent/dist
COPY --from=agent /src/agent/node_modules /agent/node_modules
ENV PORT=8080 FLEETOPS_ASSETS_DIR=/web FLEETOPS_OPERATIONS_AGENT=/agent/dist/operations-worker.js
USER node
EXPOSE 8080
ENTRYPOINT ["/fleetops"]

FROM scratch
COPY --from=backend /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=backend /fleetops /fleetops
COPY --from=backend /healthcheck /healthcheck
COPY --from=web /src/web/dist /web
ENV PORT=8080 FLEETOPS_ASSETS_DIR=/web
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/fleetops"]
