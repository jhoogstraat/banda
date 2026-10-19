# The control plane: Go API serving the static web build.
FROM node:26-slim AS web
RUN npm install -g pnpm@12
WORKDIR /web
COPY web/package.json web/pnpm-lock.yaml web/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY web ./
RUN pnpm build

FROM golang:1.27.2 AS api
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY gen gen
RUN CGO_ENABLED=0 go build -o /api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=api /api /api
COPY --from=web /web/build /web
ENV WEB_DIR=/web
ENTRYPOINT ["/api"]
