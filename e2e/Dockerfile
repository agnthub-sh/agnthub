# Containers for the agnthub end-to-end suite: one target per Harness, each with the
# real Harness CLI at a pinned version plus the agnthub binary and the compiled e2e
# test binary built from this checkout. See CONTRIBUTING.md ("End-to-end suite").
#
#   docker build -f e2e/Dockerfile --target e2e-claude --build-arg CLAUDE_VERSION=2.1.309 .
#
# Every *_VERSION accepts "latest" (the nightly job uses it).

ARG GO_VERSION=1.27
ARG NODE_VERSION=24

ARG CLAUDE_VERSION=2.1.309
ARG OPENCODE_VERSION=2.1.3
ARG CODEX_VERSION=0.164.0
ARG COPILOT_VERSION=1.0.105
ARG CURSOR_VERSION=2026.10.22-27dcf5f
ARG GEMINI_VERSION=0.66.0
ARG OMP_VERSION=18.13.3
ARG BUN_VERSION=1.4.4
# skills.sh, the default skill installer, for Skill Dependencies.
ARG SKILLS_VERSION=1.7.4

# --- agnthub and the e2e test binary -------------------------------------------------
FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/agnthub ./cmd/agnthub \
 && CGO_ENABLED=0 go test -c -tags e2e -o /out/e2e.test ./e2e

# --- shared runtime: git (served over HTTPS to the CLI), Node.js, skills.sh --------
FROM node:${NODE_VERSION}-bookworm-slim AS base
ARG SKILLS_VERSION
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git unzip xz-utils procps \
 && rm -rf /var/lib/apt/lists/*
ENV NO_UPDATE_NOTIFIER=1 NPM_CONFIG_UPDATE_NOTIFIER=false NPM_CONFIG_FUND=false
RUN npm install -g "skills@${SKILLS_VERSION}"

FROM base AS harness-claude
ARG CLAUDE_VERSION
RUN npm install -g "@anthropic-ai/claude-code@${CLAUDE_VERSION}" && claude --version

FROM base AS harness-opencode
ARG OPENCODE_VERSION
# The official OpenCode 2 installer; it installs into $HOME/.opencode/bin.
RUN set -eux; \
    if [ "$OPENCODE_VERSION" = latest ]; then set --; else set -- --version "$OPENCODE_VERSION"; fi; \
    curl -fsSL https://opencode.ai/v2/install | HOME=/opt/opencode bash -s -- --no-modify-path "$@"; \
    ln -s /opt/opencode/.opencode/bin/opencode /usr/local/bin/opencode; \
    opencode --version

FROM base AS harness-codex
ARG CODEX_VERSION
RUN npm install -g "@openai/codex@${CODEX_VERSION}" && codex --version

FROM base AS harness-copilot
ARG COPILOT_VERSION
RUN npm install -g "@github/copilot@${COPILOT_VERSION}" && copilot --version

FROM base AS harness-cursor
ARG CURSOR_VERSION
# A pinned version downloads that build directly (as the official installer does);
# "latest" runs the official installer.
RUN set -eux; \
    case "$(uname -m)" in x86_64) arch=x64 ;; aarch64) arch=arm64 ;; *) exit 1 ;; esac; \
    if [ "$CURSOR_VERSION" = latest ]; then \
      curl -fsSL https://cursor.com/install | HOME=/opt/cursor bash; \
      dir="$(dirname "$(readlink -f /opt/cursor/.local/bin/cursor-agent)")"; \
    else \
      dir="/opt/cursor/versions/$CURSOR_VERSION"; mkdir -p "$dir"; \
      curl -fsSL "https://downloads.cursor.com/lab/$CURSOR_VERSION/linux/$arch/agent-cli-package.tar.gz" \
        | tar --strip-components=1 -xzf - -C "$dir"; \
    fi; \
    ln -s "$dir/cursor-agent" /usr/local/bin/cursor-agent; \
    cursor-agent --version

FROM base AS harness-gemini
ARG GEMINI_VERSION
RUN npm install -g "@google/gemini-cli@${GEMINI_VERSION}" && gemini --version

FROM base AS harness-omp
ARG BUN_VERSION
ARG OMP_VERSION
ENV BUN_INSTALL=/opt/bun PATH=/opt/bun/bin:$PATH
RUN npm install -g "bun@${BUN_VERSION}" \
 && bun install -g "@oh-my-pi/pi-coding-agent@${OMP_VERSION}" \
 && omp --version

# --- one runnable image per Harness ---------------------------------------------------
FROM harness-claude AS e2e-claude
ENV E2E_HARNESS=claude
COPY --from=build /out/ /usr/local/bin/
WORKDIR /e2e
ENTRYPOINT ["e2e.test"]

FROM harness-opencode AS e2e-opencode
ENV E2E_HARNESS=opencode
COPY --from=build /out/ /usr/local/bin/
WORKDIR /e2e
ENTRYPOINT ["e2e.test"]

FROM harness-codex AS e2e-codex
ENV E2E_HARNESS=codex
COPY --from=build /out/ /usr/local/bin/
WORKDIR /e2e
ENTRYPOINT ["e2e.test"]

FROM harness-copilot AS e2e-copilot
ENV E2E_HARNESS=copilot
COPY --from=build /out/ /usr/local/bin/
WORKDIR /e2e
ENTRYPOINT ["e2e.test"]

FROM harness-cursor AS e2e-cursor
ENV E2E_HARNESS=cursor
COPY --from=build /out/ /usr/local/bin/
WORKDIR /e2e
ENTRYPOINT ["e2e.test"]

FROM harness-gemini AS e2e-gemini
ENV E2E_HARNESS=gemini
COPY --from=build /out/ /usr/local/bin/
WORKDIR /e2e
ENTRYPOINT ["e2e.test"]

FROM harness-omp AS e2e-omp
ENV E2E_HARNESS=omp
COPY --from=build /out/ /usr/local/bin/
WORKDIR /e2e
ENTRYPOINT ["e2e.test"]
