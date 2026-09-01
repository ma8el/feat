# ADR-048 — Removing the external resource declaration

Status: accepted  
Recorded: 2026-08-09, after use

`runtime.external_resources` is the one part of the runtime model drawn from the
reference project's shape rather than from the product's. The maintainer, asking
what it is for, gave the answer that settles it: which database an application
connects to is the user's deliberate choice, expressed in a connection string
inside an environment file, and Feat has no way of knowing what is at the other
end of it.

Evidence:

1. Feat cannot see the resource and is forbidden from trying. The connection
   string lives in an environment file, which `checkEnvFiles` stats and never
   opens because [05-security-model.md](05-security-model.md) requires it, and
   which the runtime adapter passes to Compose by path for the same reason. The
   declaration therefore names something Feat has committed never to look at, and
   no code path can ever reconcile the two.
2. The check that reports on it cannot fail. `feat doctor` stats every configured
   Compose file, asks `docker compose config --services` whether each managed
   service is really defined, and stats each environment file and reports its
   permissions. For an external resource it emits an unconditional pass carrying
   the words "referenced, never created or destroyed by Feat" — the only runtime
   check in `internal/project/checks.go` that makes a claim about a user's
   resource without contacting it.
3. Its whole runtime effect duplicates a variable that is already set.
   `FEAT_TASK_KEY` is generated for every managed service unconditionally, and an
   external resource's `selector_variable` is given the task key — the identical
   value. The entire behaviour of the feature is that the variable may be called
   something else. A static override cannot do the same, because it is one file
   for every task and cannot hold a per-task value, so the aliasing is real; it is
   also all there is.
4. The safety it appears to provide comes from somewhere else. Destroy runs
   `down` on the task's own Compose project and volume removal enumerates by
   Compose label, so a resource outside that project is excluded by construction
   — the rule `removeVolumes` records, and the reason no cleanup path reads the
   recorded list. `validateExternal` refuses an external resource whose
   identifier is also a managed service, which guards a project that runs the
   resource in Compose while declaring that Feat must not touch it; for a
   resource that is not in Compose at all, the branch cannot fire.
5. The record has two readers and both are display. `ExternalResources` is
   validated, persisted, published, and rendered by `feat runtime status` and the
   runtime panel. `domain.ExternalResource` says its lifecycle is recorded so
   that a cleanup plan can prove it excluded the resource; no cleanup plan reads
   it.
6. The lifecycle it records has one inhabitant. `LifecycleManaged` appears in
   non-test code only in its own declaration and in `Valid()`. Nothing is ever
   recorded as managed, so the enum draws a distinction that only ever has one
   side.

Decisions:

- `runtime.external_resources` is removed from the configuration model, the JSON
  schema, the domain, the store, the API, and both display surfaces.
- `FEAT_TASK_KEY` is the mechanism, and is documented as such in
  [07-configuration-model.md](07-configuration-model.md) rather than left
  implicit. Feat sets it on every managed service; a project that shares one
  resource between tasks uses it to name its share; Feat neither knows nor asks
  what is behind the name. Removing a documented feature and replacing it with an
  undocumented convention would be worse than either.
- FR-RUN-008 is amended rather than dropped. Allowing an external resource means
  not interfering with one: Feat supplies a per-task discriminator, reads no
  environment file, and models nothing about the resource. Provisioning,
  migration, seeding, and reclamation stay out of v0.
- Nothing reports on a resource Feat cannot reach. Naming an unreclaimed share in
  a cleanup plan was considered and refused. It would assert the existence of
  something Feat has never contacted, on the user's word alone, at the moment a
  user is deciding whether it is safe to proceed — the same unverifiable claim as
  evidence 2, moved somewhere it would carry more weight. A share on a server Feat
  cannot see is the user's to reclaim, and saying so in a plan does not make it
  Feat's.

Consequence: about 240 lines across twelve non-test files and seven test files
are deleted, together with the `externalResource` schema definition, the example
in [07-configuration-model.md](07-configuration-model.md), and the block in
`docs/examples/project.yaml`. No stored snapshot needs migrating: the field is
written `omitempty` and task documents are decoded without
`DisallowUnknownFields`, so the key in an existing snapshot is ignored. A project
that shared a staging database by `selector_variable` reads `FEAT_TASK_KEY`
instead, in its own configuration or in a container entrypoint that re-exports
it. The generated Compose override loses nothing: the variable it carried is
still there under the name Feat generates.
