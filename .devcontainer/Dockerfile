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
# The agent's user, and a way back to root that this image grants on purpose.
#
# `NOPASSWD` is what lets a session `apt-get install` something the image does
# not carry, which is why it is here. It is also the reason "the container user
# is non-root" is true of the moment the launch measures it and not of the
# session: with the runtime's default capabilities, root in here writes through
# every writable bind mount whatever owns the files on the host — and does not
# reach a read-only one, which needs CAP_SYS_ADMIN that the default set excludes.
#
# Feat probes for this at every launch and reports it against the task, so it is
# a grant that says its own name rather than one nobody sees again. An image
# that does not need it should drop `sudo` from the packages above and these two
# lines; docs/05-security-model.md § What "non-root" is a statement about has
# the reasoning.
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
