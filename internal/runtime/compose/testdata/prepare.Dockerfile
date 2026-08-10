# The one-shot dependency of the opt-in fixture, built rather than pulled.
#
# It exists so that one image in the task's Compose project has to be built
# before a container can be created from it, which is the difference between
# `docker compose create` and what a create means (ADR-034 evidence 13). It adds
# one trivial layer over the image the fixture already uses, so a machine that
# can run the suite at all needs nothing more to build it.
FROM alpine:3.20
RUN touch /prepared
