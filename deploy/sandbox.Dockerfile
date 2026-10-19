FROM golang:1.27.2 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY gen gen
RUN CGO_ENABLED=0 go build -o /runner ./cmd/runner

FROM debian:trixie-slim AS sandbox
ARG CLAUDE_VERSION=2.1.301
ENV DISABLE_AUTOUPDATER=1
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates curl ripgrep jq make procps python3 nodejs npm \
 && rm -rf /var/lib/apt/lists/* \
 && curl -fsSL https://claude.ai/install.sh | bash -s "$CLAUDE_VERSION" \
 && cp -L /root/.local/bin/claude /usr/local/bin/claude \
 && rm -rf /root/.local /root/.claude /root/.claude.json \
 && claude --version
RUN mkdir -p /etc/claude-code \
 && echo '{"cleanupPeriodDays": 3650}' > /etc/claude-code/managed-settings.json
RUN groupadd -g 1000 banda && useradd -u 1000 -g 1000 -m -d /home/banda banda
COPY --from=build /runner /usr/local/bin/runner
USER 1000
WORKDIR /home/banda
ENTRYPOINT ["/usr/local/bin/runner"]

# Test image: a fake claude that needs no login (see deploy/fake-claude).
FROM sandbox AS fake
COPY deploy/fake-claude /usr/local/bin/claude
