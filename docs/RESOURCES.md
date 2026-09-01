# Fly.io Resource Catalog

Namespace: **`FLY`** · Plugin: `formae-plugin-fly` · Researched 2026-08-21 against the
live Fly.io API (not from memory, not from a Terraform provider).

Ground truth for everything below:

| Source | What it gave us |
|--------|-----------------|
| `https://docs.machines.dev/spec/openapi3.json` (OpenAPI 3.0.1, `info.version` 1.0, 140 KB) | The Machines REST surface: 68 paths, 98 operations, 173 schemas |
| `POST https://api.fly.io/graphql` introspection | The GraphQL surface: 396 types, 93 mutations |
| Unauthenticated route probes against `https://api.machines.dev` | Which documented routes actually exist (401 = exists, 404 = does not) |
| `superfly/flyctl` `internal/config/config.go` | Exact credential env-var precedence |

---

## Phase 0 — Ecosystem cross-check

There is **no official Fly.io Terraform provider and no Pulumi provider.** Fly.io
publishes `flyctl` and the APIs; IaC coverage is entirely community-built. That makes
the Fly.io API docs — specifically the OpenAPI spec above — the ground truth for this
plugin. Community providers were used only as a hint for *which* resources are
modelable and what field names look like.

### Community Terraform providers

Snapshot: `/Users/stheno/git/pel/plugins/plugin-factory/data/terraform_*.csv` (dated
2026-06-24). Live column re-counted 2026-08-21 by listing `docs/resources/` and
`docs/data-sources/` via the GitHub API.

| Provider | Downloads (snapshot) | Resources (snapshot) | Resources (live) | Data sources (live) | Last push | Stars | State |
|----------|---------------------:|---------------------:|-----------------:|--------------------:|-----------|------:|-------|
| `andrewbaxter/fly` | 44,263 | 9 (res+ds combined) | **5** | 4 | 2025-02-18 | 27 | active, most-used |
| `stategraph/fly` | 733 | 47 (res+ds combined) | **29** | 18 | 2026-07-23 | 1 | **archived** |
| `DAlperin/fly-io` | 18,100 | 8 | 5 | 3 | 2023-01-31 | 8 | stale |
| `getenv/fly` | 7,592 | — | *no docs dir* | — | 2023-03-14 | 0 | stale |
| `floydspace/fly` | 5,524 | 9 | 5 | 4 | 2023-04-09 | 0 | stale |
| `pi3ch/fly` | 4,275 | 9 | 5 | 4 | 2023-12-05 | 6 | stale |
| `Virtual-Repetitions/fly` | 2,829 | 8 | 5 | 3 | — | — | fork |
| `eldios/fly` | 2,543 | 8 | 5 | 3 | 2023-01-24 | 0 | fork |
| `damianstasik/fly` | 1,281 | 8 | 5 | 3 | 2023-04-05 | 0 | fork |
| `kinok/fly` | 454 | 9 | 5 | 4 | 2025-09-02 | 2 | fork |

**Corrections to the snapshot.** The CSV counts `docs/resources/` and
`docs/data-sources/` entries together. Live per-directory counts:
`andrewbaxter/fly` has **5** resources (`app`, `cert`, `ip`, `machine`, `volume`) —
matching the brief — and `stategraph/fly` has **29** resources + 18 data sources, not
"~25". `getenv/fly` publishes no `docs/` tree at all, which is why the resource CSV has
zero rows for it.

`stategraph/fly` — the widest surface, and the one worth reading for field naming — was
**archived on 2026-07-23**. Everything except `andrewbaxter/fly` (Feb 2025) and
`kinok/fly` (Sep 2025) has been untouched since 2023. Seven of the ten providers are
byte-identical 5-resource forks of the same `app/cert/ip/machine/volume` lineage.

`stategraph/fly`'s 29 resources, for reference: `app`, `certificate`, `egress_ip`,
`ext_arcjet`, `ext_kubernetes`, `ext_mysql`, `ext_sentry`, `ext_vector`, `ext_wafris`,
`ip_address`, `litefs_cluster`, `machine`, `mpg_attachment`, `mpg_cluster`,
`mpg_database`, `mpg_user`, `network_policy`, `org`, `org_member`,
`postgres_attachment`, `postgres_cluster`, `redis`, `secret`, `tigris_bucket`, `token`,
`volume`, `volume_snapshot`, `wireguard_peer`, `wireguard_token`.

### The Fly.io API surface, counted ourselves

**Machines REST API** (`https://api.machines.dev`, OpenAPI 3.0.1):
**68 paths, 98 operations, 173 schemas.**

| Endpoint group | Operations | Declarative? |
|----------------|-----------:|--------------|
| `machines` (create/get/update/destroy + start/stop/suspend/restart/signal/cordon/uncordon/exec/ps/lease/metadata/memory/events/versions/wait) | 29 | ~7 declarative, ~22 runtime |
| `postgres` (Managed Postgres clusters, databases, users, extensions, backups, attachments, fork, restore) | 22 | mostly declarative |
| `secretkeys` (KMS-style keys + encrypt/decrypt/sign/verify/generate) | 9 | 4 declarative, 5 runtime crypto ops |
| `certificates` (list/get/acme/custom/check/remove) | 8 | 6 declarative, 2 runtime |
| `volumes` (create/get/update/destroy/extend/snapshots) | 8 | 6 declarative, 2 runtime |
| `secrets` (app secrets: bulk set, per-name get/set/delete) | 5 | declarative |
| `tokens` (authenticate/authorize/current/kms/oidc) | 5 | 0 declarative — all runtime |
| `apps` (list/create/get/destroy) | 4 | declarative |
| `ip_assignments` (list/assign/release) | 3 | declarative |
| `orgs` (`/v1/orgs/{slug}/machines`, `/volumes` — cross-app listing) | 2 | read-only, useful for discovery |
| `platform` (`/regions`, `/placements`) | 2 | read-only reference data |
| `deploy_token` (mint an app-scoped deploy token) | 1 | runtime |
| **Total** | **98** | **~54 declarative (55%), ~44 runtime (45%)** |

The runtime half is why this is not a thin CRUD wrapper: nearly half the Machines API is
imperative verbs (`start`, `stop`, `exec`, `signal`, `restart`, `lease`, `wait`,
`encrypt`, `authorize`) that have no place in a desired-state model. Formae models the
~54 declarative operations; the imperative ones are used *internally* by the plugin
(notably `wait`, `start`, `stop` during Machine create/replace) or not at all.

**GraphQL API** (`https://api.fly.io/graphql`, live, unversioned, undocumented):
**396 types (254 objects, 104 inputs, 24 enums), 93 mutations.** Introspection is open
without auth. It is the older surface and still the *only* place for: organizations
(`createOrganization`, `deleteOrganization`, `deleteOrganizationMembership`), WireGuard
peers, egress IPs (`allocateEgressIpAddress`), add-ons — Upstash Redis / Tigris buckets
(`createAddOn`), builds/releases, and legacy Nomad-era scaling (`scaleApp`, `setVmSize`,
`updateAutoscaleConfig`).

**Everything this plugin needs is on the REST API.** See ARCHITECTURE.md § Transport for
the decision and the evidence; the short version is that certificates and IP assignments
— which the 2023-era community providers had to reach through GraphQL, and which the
brief expected to be GraphQL-only — are now first-class REST endpoints, verified live.

---

## Catalog

Legend — CRUD: **C**reate **R**ead **U**pdate **D**elete **L**ist.
Priority: **P1** implemented now · **P2** next · **P3** later/maybe never.
Transport: **REST** = `api.machines.dev` · **GQL** = `api.fly.io/graphql`.

### Apps — implemented (P1)

| Resource type | Transport | Endpoint | CRUD | P | Modeled by |
|---------------|-----------|----------|------|---|------------|
| `FLY::Apps::App` | REST | `/v1/apps`, `/v1/apps/{app}` | C R D L | P1 | all 10 providers (`fly_app`) |
| `FLY::Apps::Machine` | REST | `/v1/apps/{app}/machines[/{id}]` | C R U D L | P1 | all 10 (`fly_machine`) |
| `FLY::Apps::Volume` | REST | `/v1/apps/{app}/volumes[/{id}]` | C R U D L | P1 | all 10 (`fly_volume`) |
| `FLY::Apps::Secrets` | REST | `/v1/apps/{app}/secrets` | C R U D L | P1 | `stategraph` (`fly_secret`) |
| `FLY::Apps::Certificate` | REST | `/v1/apps/{app}/certificates[/acme\|/{hostname}]` | C R D L | P1 | all 10 (`fly_cert`/`fly_certificate`) |
| `FLY::Apps::IPAddress` | REST | `/v1/apps/{app}/ip_assignments[/{ip}]` | C R D L | P1 | all 10 (`fly_ip`/`fly_ip_address`) |

Supported `addressType` values are `shared_v4`, `v4` and `v6`. The API also accepts
`private_v6` and the plugin can create one, but it is not advertised as supported: the
assignment listing reports only the address and a `shared` flag, never the requested type,
so `Read` must infer the type from the address family — and a private 6PN address is IPv6,
so it would read back as `v6` and drift forever. The three supported values round-trip
exactly.

No `FLY::Apps::App` **U**: the REST API exposes no app-update endpoint. Every App field
is `createOnly`; a change replaces the app. Same for Certificate and IPAddress — both are
create-or-destroy only server-side.

### Postgres (Managed Postgres) — implemented

The single largest unclaimed area: 22 REST operations, and `/v1/postgres` is a *newer*
top-level REST group (not app-scoped), confirmed live (unauthenticated `GET /v1/postgres`
returns `{"error":"Organization not found"}` — the route exists).

| Resource type | Transport | Endpoint | CRUD | P | Modeled by |
|---------------|-----------|----------|------|---|------------|
| `FLY::Postgres::Cluster` | REST | `/v1/postgres[/{id}]` | C R D L | P1 | `stategraph` (`fly_mpg_cluster`) |
| `FLY::Postgres::Database` | REST | `/v1/postgres/{id}/databases[/{name}]` | C R D L | P1 | `stategraph` (`fly_mpg_database`) |
| `FLY::Postgres::User` | REST | `/v1/postgres/{id}/users[/{name}]` | C R U D L | P1 | `stategraph` (`fly_mpg_user`) |
| `FLY::Postgres::Attachment` | REST | `/v1/postgres/{id}/attachments[/{app}]` | C R D L | P1 | `stategraph` (`fly_mpg_attachment`) |
| `FLY::Postgres::Extension` | REST | `/v1/postgres/{id}/databases/{db}/extensions` | C R D L | P1 | — |
| `FLY::Postgres::Backup` | REST | `/v1/postgres/{id}/backups` | C R **D\*** L | P1 | — |

**\*** Backup has no delete endpoint — see "Resources that cannot be deleted" below.

`Cluster` create is genuinely async: `status` walks
`creating → initializing → ready` (also `deleting`/`deleted`/`failed`), and `endpoints`
is only populated at `ready`. `plan` is one of `basic|starter|launch|scale|Performance`
(the capital *P* is in the spec — not a typo on our side), `pg_major_version` `16|17`,
`disk_size_gb` 10–1000.

### Volume snapshots, secret keys — implemented

| Resource type | Transport | Endpoint | CRUD | P | Modeled by |
|---------------|-----------|----------|------|---|------------|
| `FLY::Apps::VolumeSnapshot` | REST | `/v1/apps/{app}/volumes/{id}/snapshots` | C R **D\*** L | P1 | `stategraph` (`fly_volume_snapshot`) |
| `FLY::Apps::SecretKey` | REST | `/v1/apps/{app}/secretkeys[/{name}]` | C R U D L | P1 | — |

### Resources that cannot be deleted

`FLY::Postgres::Backup` and `FLY::Apps::VolumeSnapshot` have **no delete endpoint**.
Fly offers no way to remove either; both expire under a retention policy. Their `Delete`
therefore reports Success with a `StatusMessage` saying the artifact remains — failing
would wedge every `formae destroy` containing one, and bare success would hide it.

Both are also server-id-assigned, so a forma cannot name one: re-applying after a destroy
takes a *new* backup or snapshot rather than reconciling to the existing one. They suit
"take a backup at apply time" in a pipeline; for scheduled protection use the cluster's
retention policy or the volume's `autoBackupEnabled`.

This is the declarative REST surface exhausted. Everything still unimplemented needs a
GraphQL client.

`SecretKey` is a different thing from `Secrets`: it is app-scoped KMS key material used by
the `encrypt`/`decrypt`/`sign`/`verify` endpoints, not an env-var secret.

### GraphQL-only — P3, and a second transport is the price

Not implemented, and deliberately not planned for v1: each of these forces a GraphQL
client into the plugin. Listed so the next person knows the cost is a transport, not a
resource.

| Resource type | Transport | GraphQL mutation | P | Modeled by |
|---------------|-----------|------------------|---|------------|
| `FLY::Platform::Organization` | GQL | `createOrganization` / `deleteOrganization` | P3 | `stategraph` (`fly_org`) |
| `FLY::Platform::OrgMember` | GQL | `deleteOrganizationMembership` / `updateOrganizationMembership` | P3 | `stategraph` (`fly_org_member`) |
| `FLY::Network::EgressIP` | GQL | `allocateEgressIpAddress` | P3 | `stategraph` (`fly_egress_ip`) |
| `FLY::Network::WireGuardPeer` | GQL | `addWireGuardPeer` | P3 | `stategraph` (`fly_wireguard_peer`) |
| `FLY::Addons::Redis` (Upstash) | GQL | `createAddOn` | P3 | `stategraph` (`fly_redis`) |
| `FLY::Addons::TigrisBucket` | GQL | `createAddOn` | P3 | `stategraph` (`fly_tigris_bucket`) |
| `FLY::Platform::Token` | GQL | `createLimitedAccessToken` | P3 | `stategraph` (`fly_token`) |

### Not modelable as resources

Runtime verbs and read-only reference data — correctly absent from a desired-state model:
machine `start`/`stop`/`suspend`/`restart`/`signal`/`cordon`/`exec`/`ps`/`lease`/`wait`/
`events`/`versions`/`memory`; `/v1/platform/regions` and `/v1/platform/placements`
(reference data — use `formae` variables or read them at plan time); all five `/v1/tokens/*`
operations; `/v1/apps/{app}/deploy_token`; the `secretkeys` crypto operations.

`stategraph`'s `fly_ext_*` resources (Sentry, Vector, Arcjet, Wafris, MySQL, Kubernetes)
are partner-extension provisioning via GraphQL `createAddOn`. Out of scope.

---

## Conformance test selection

The harness applies `testdata/resource.pkl` → `resource-update.pkl` → `resource-replace.pkl`
and then destroys, per resource, per run. So cost and wall-clock matter more than coverage.

| Resource | Cost per cycle | Wall clock | In conformance? |
|----------|----------------|-----------|-----------------|
| `FLY::Apps::App` | **$0** — an app with no machines bills nothing | seconds | **yes, primary** |
| `FLY::Apps::Secrets` | **$0** | seconds | **yes, secondary** |
| `FLY::Apps::Machine` | ~$0.0000008/s for `shared-cpu-1x`/256 MB — cents per run | 30–90 s create, needs `TIMEOUT=15` | **yes, third** (user-approved) |
| `FLY::Apps::Volume` | 1 GB provisioned storage, prorated | seconds | no — leaves billable storage if a run aborts |
| `FLY::Apps::IPAddress` | dedicated IPv4 is billable; shared IPv4 and IPv6 are free | seconds | no — not worth a lifecycle test |
| `FLY::Apps::Certificate` | $0 | never converges | **no** — ACME validation needs DNS records on a domain we control; `status` stays `pending_validation` forever in CI |

`clean-environment.sh` deletes any app whose name starts with the test prefix, which
cascades machines, volumes, secrets, certs and IP assignments — one `DELETE /v1/apps/{app}`
cleans up everything a failed run could have left behind.
