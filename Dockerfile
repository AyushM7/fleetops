FROM node:24.21.0 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.1 AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -p 1 -trimpath -o /fleetops ./cmd/server
RUN CGO_ENABLED=0 go build -p 1 -trimpath -o /healthcheck ./cmd/healthcheck

FROM scratch
COPY --from=backend /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=backend /fleetops /fleetops
COPY --from=backend /healthcheck /healthcheck
COPY --from=web /src/web/dist /web
ENV PORT=8080 FLEETOPS_ASSETS_DIR=/web
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/fleetops"]
