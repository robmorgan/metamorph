FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
    git \
    curl \
    jq \
    gettext-base \
    build-essential \
    ca-certificates \
    gnupg \
    && mkdir -p /etc/apt/keyrings \
    && curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
       | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg \
    && echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_22.x nodistro main" \
       > /etc/apt/sources.list.d/nodesource.list \
    && apt-get update \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

ARG EXTRA_PACKAGES=""
RUN if [ -n "$EXTRA_PACKAGES" ]; then \
    apt-get update && apt-get install -y $EXTRA_PACKAGES \
    && rm -rf /var/lib/apt/lists/*; \
    fi

RUN npm install -g @anthropic-ai/claude-code

RUN userdel -r ubuntu 2>/dev/null; useradd -m -s /bin/bash -u 1000 agent

COPY SYSTEM_PROMPT.md /SYSTEM_PROMPT.md
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

RUN mkdir -p /workspace && chown agent:agent /workspace
WORKDIR /workspace

USER agent

ENTRYPOINT ["/entrypoint.sh"]
