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
# Tools from the repo's mise config live on the home volume.
ARG MISE_VERSION=2026.10.13
ENV MISE_DATA_DIR=/home/banda/.local/share/mise
ENV PATH=$MISE_DATA_DIR/shims:$PATH
RUN curl -fsSL https://mise.run | MISE_VERSION=v$MISE_VERSION MISE_INSTALL_PATH=/usr/local/bin/mise sh \
 && mise --version \
 && echo 'export PATH="$MISE_DATA_DIR/shims:$PATH"' > /etc/profile.d/mise.sh
RUN mkdir -p /etc/claude-code \
 && echo '{"cleanupPeriodDays": 3650}' > /etc/claude-code/managed-settings.json
RUN groupadd -g 1000 banda && useradd -u 1000 -g 1000 -m -d /home/banda banda
COPY --from=build /runner /usr/local/bin/runner
USER 1000
WORKDIR /home/banda
ENTRYPOINT ["/usr/local/bin/runner"]

# Test image: a fake claude that needs no login (see deploy/fake-claude), and
# repos for e2e at file:///opt/e2e/<name>.git: setup-fails (its .banda/setup.sh
# fails) and setup-bg (it succeeds, leaving a background process).
FROM sandbox AS fake
COPY deploy/fake-claude /usr/local/bin/claude
USER root
RUN repo() { git init -q -b main /tmp/$1 && mkdir /tmp/$1/.banda && printf "$2" > /tmp/$1/.banda/setup.sh \
   && git -C /tmp/$1 add -A && git -C /tmp/$1 -c user.name=e2e -c user.email=e2e@localhost commit -qm init \
   && git clone -q --bare /tmp/$1 /opt/e2e/$1.git && rm -rf /tmp/$1; } \
 && repo setup-fails 'echo "setup sees $E2E_SECRET"\nexit 7\n' \
 && repo setup-bg 'sleep 600 &\necho setup-bg-done\n' \
 && chown -R 1000:1000 /opt/e2e
USER 1000
