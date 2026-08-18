# The image Feat runs its own agent tasks in.

FROM golang:1.26

RUN apt-get update && apt-get install -y --no-install-recommends \
        nodejs \
        npm \
        make \
        sudo \
    && rm -rf /var/lib/apt/lists/*

# The GitHub CLI from its own repository rather than Debian's, which lags far
# enough behind that subcommands an agent reaches for are missing. The project
# configures `github_cli: optional`, so `feat doctor` reports what it finds here
# and a launch is never blocked on it.
RUN mkdir -p -m 755 /etc/apt/keyrings \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        -o /etc/apt/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

ARG UID=1000
ARG GID=1000
RUN groupadd --gid "${GID}" dev || true \
    && useradd --create-home --shell /bin/bash --uid "${UID}" --gid "${GID}" dev \
    && echo "dev ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/dev \
    && chmod 0440 /etc/sudoers.d/dev

RUN mkdir -p /go/pkg/mod /home/dev/.cache/go-build \
    && chown -R dev:dev /go /home/dev/.cache

RUN printf 'export PATH=/usr/local/go/bin:/go/bin:$PATH\n' > /etc/profile.d/go.sh

RUN npm install -g @anthropic-ai/claude-code

USER dev
WORKDIR /home/dev

RUN claude --version && go version && git --version && make --version && gh --version
