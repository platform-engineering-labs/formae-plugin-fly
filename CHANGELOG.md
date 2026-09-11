# Changelog

All notable changes to `formae-plugin-fly`.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versioning follows [semver](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — unreleased

First release. Requires formae 0.89.0 or newer.

### Fixed

- Machine discovery found nothing. `Machine.List` used
  `GET /v1/orgs/{org}/machines`, which Fly documents as "a point in time" whose
  "recent machine changes, including creations and destructions, may take time
  to propagate" — in practice a machine created seconds earlier is absent from
  it for minutes, so every discovery scan reported zero machines while `Read`
  answered for the same machine immediately. `List` now fans out over the org's
  apps and uses the per-app endpoint, which is immediately consistent. That is
  N+1 requests instead of one, the same trade `Secrets.List` already makes.
  Volume keeps the org-wide endpoint: it carries no such caveat and discovery
  finds a fresh volume on the first scan.

### Adopted the formae 0.89.0 secret model

- `FLY::Apps::Secrets.values` entries and `FLY::Apps::SecretKey.value` are typed
  `formae.ValueSource`, so either can be bound to a `PasswordGenerator` /
  `KeyPairGenerator` output, or to another provider's secret
  (`secret.res.secretValue`, `.at(key)`, `.json(path)`), instead of a literal.
  A referenced secret is re-read from its provider on every plugin call, so
  rotating it upstream needs no re-apply here.
- `SecretKey.value` is now hashed at rest: it is a scalar, so the
  `formae.SecretValue` arm of `ValueSource` makes the rendered FieldHint opaque
  and `formae.value(x).opaque` on it is redundant. `Secrets.values` entries are
  not, because formae derives opacity from a field's declared type and does not
  descend into map value positions — keep using `formae.value(x).opaque` there.
- `FLY::Apps::Secrets` is a first-class secret resource (`formae.Secret` +
  `MapSecretResolvable`), so an entry is reachable as
  `bag.res.secretValue.at("KEY")` — including the `DATABASE_URL` that
  `FLY::Postgres::Attachment` injects, which formae never wrote and could not
  otherwise reference. `Read` reveals the bag
  (`GET /v1/apps/{app}/secrets?show_secrets=true`) onto a new read-only
  `decodedValues` field; `values` stays the write side and is still never echoed
  back, so value drift remains undetected. Reveal is scoped to `Read`: `List`
  walks every app in the org during discovery and never reveals. A token that
  may list secrets but not reveal them falls back to a names-only read rather
  than failing, so read-only tokens keep working; `401` still surfaces as a
  credential error.
- Examples: the full-stack examples draw the Postgres password from a
  `formae.PasswordGenerator` bound to both Supabase's `dbPass` and the Fly
  secret, so the two are provably equal and the `SUPABASE_DB_PASS` env var is
  gone. No rotation cadence — `dbPass` is createOnly on Supabase.

### Target formae 0.89.0

- Pkl schema dependency bumped to `formae@0.89.0`; `minFormaeVersion` raised
  from `0.84.0`, since the schema now names `formae.ValueSource`.
- SDK to `pkg/plugin v0.4.2`, `pkg/model v0.1.28`,
  `pkg/plugin-conformance-tests v0.2.7`.
- The `SecretKey` conformance fixtures declare bare strings on what is now an
  opaque field: v0.2.7 verifies the stored SHA-256 digest against the authored
  plaintext, where v0.2.6 compared the two literally and failed.

### Added

Fourteen resource types, all on the Fly.io Machines REST API
(`api.machines.dev`) — the declarative REST surface in full:

| Resource | Operations |
|----------|-----------|
| `FLY::Apps::App` | Create, Read, Delete, List |
| `FLY::Apps::Machine` | Create, Read, Update, Delete, Status, List |
| `FLY::Apps::Volume` | Create, Read, Update, Delete, List |
| `FLY::Apps::Secrets` | Create, Read, Update, Delete, List |
| `FLY::Apps::Certificate` | Create, Read, Delete, List |
| `FLY::Apps::IPAddress` | Create, Read, Delete, List |
| `FLY::Apps::VolumeSnapshot` | Create, Read, Delete\*, List |
| `FLY::Apps::SecretKey` | Create, Read, Update, Delete, List |
| `FLY::Postgres::Cluster` | Create, Read, Delete, Status, List |
| `FLY::Postgres::Database` | Create, Read, Delete, List |
| `FLY::Postgres::User` | Create, Read, Update, Delete, List |
| `FLY::Postgres::Attachment` | Create, Read, Delete, List |
| `FLY::Postgres::Extension` | Create, Read, Delete, List |
| `FLY::Postgres::Backup` | Create, Read, Delete\*, List |

\* Fly exposes no delete endpoint for volume snapshots or Postgres backups.
Delete reports success and says so; the artifact expires under its retention
policy.

App, Certificate and IPAddress expose no Update: the API has no update endpoint
for any of them, so every field is `createOnly` and a change is a replacement.

- Credential resolution matching flyctl exactly: `FLY_ACCESS_TOKEN`, then
  `FLY_API_TOKEN`.
- Discovery for all six types. Volume uses the org-wide endpoint (one
  cursor-paged call); Machine, Secrets, Certificate and IPAddress fan out per
  app — for Machine because Fly's org-wide machine index lags recent creations
  by minutes, for the others because no org-wide endpoint exists.
- Conformance coverage — CRUD lifecycle and discovery — for App, Secrets,
  Machine, Volume, IPAddress and the whole Managed Postgres graph (cluster,
  database, user, extension, attachment) in one forma, so a single billable
  cluster serves every child. Ten of the fourteen types are exercised against
  the live API.

  SecretKey is covered too, so twelve of the fourteen types are exercised
  against the live API.

  Not conformance-tested, deliberately: `Certificate` never converges without
  DNS records on a domain the suite controls, and `VolumeSnapshot` /
  `Postgres::Backup` have no delete endpoint, so every run would leak an
  artifact. Reasoning in docs/RESOURCES.md.
- `examples/basic/` — one publicly reachable Fly app.
- `examples/fullstack-fly-supabase-vercel/` — a three-tier application across
  Fly, Supabase and Vercel wired with cross-plugin resolvables, plus a
  two-provider variant.
- `docs/RESOURCES.md` and `docs/ARCHITECTURE.md` — the API catalog, the counts
  behind it, and the design decisions.

### Notes and known limitations

- **Rate limit is 3 requests/second namespace-wide**, Fly's documented burst.
  It began at 1 and that was too conservative: machine discovery timed out
  against the conformance harness's 2-minute window because a discovery sweep
  serialises dozens of requests across 14 resource types, five of which fan out
  per app or per cluster. No 429s were observed at either setting. Formae's
  limiter has one per-namespace knob, so it cannot express Fly's real
  per-action, per-object limits.

- **Single transport.** Everything is REST. Certificates and IP assignments are
  first-class REST endpoints now, so no GraphQL client is needed; the resources
  that would require one (organizations, WireGuard peers, egress IPs, Upstash
  and Tigris add-ons) are all P3 and unimplemented.
- **`Machine.state` is an output, not desired state.** Setting it does nothing.
  fly-proxy stops and starts machines itself under `autostop`/`autostart`, so
  reconciling `state` would fight the platform in a loop that bills for every
  wake-up.
- **A changed secret does not reach a running machine** until it restarts. The
  plugin does not restart machines as a side effect of a secret update.
- **Certificate create does not wait for validation.** ACME validation needs DNS
  records only the user can publish, so `status` can stay `pending_validation`
  indefinitely; the records are surfaced in the `dnsRequirements` output.
- **`Secrets.values` is write-only.** Value drift cannot be detected — only an
  added or removed name. Read reveals the bag onto the separate read-only
  `decodedValues` field, which exists to resolve `secretValue` references, not
  to diff. Per-entry opacity on `values` is available via
  `formae.value(x).opaque`; a field-level `opaque` hint is not expressible for a
  map-valued field on formae 0.89.0.
- **`SecretKey.keyType` is required and immutable.** The OpenAPI spec marks it
  optional; omitting it answers 400 and lists the accepted set (`hs256`,
  `hs384`, `hs512`, `xaes256gcm`, `nacl_auth`, `nacl_box`, `nacl_secretbox`,
  `nacl_sign`, `es256`). Changing the type of an existing key answers
  `500 secret <name> is of type 3, expected 5`, so it is createOnly and formae
  replaces instead of updating. Key names also reject hyphens.
- **`org_slug` is never sent when allocating an IP address.** The API declares
  the field but rejects it for every type except `private_v6`
  (`400 org_slug is only supported with private_v6 type`), and `private_v6` is
  not supported here. The spec does not mention the restriction.
- **A deleted volume does not 404.** The API answers 200 with the volume still
  present and `state: "waiting_for_detach"`, so Read treats that and the other
  outgoing states as gone — otherwise formae's sync never prunes it.
- **`org = "personal"` is refused.** Fly accepts the alias on create and reports
  the organization's real slug on read; since `App.org` is `createOnly`, that
  would be drift on an immutable field and a replacement on every reconcile.
  Use the real slug. Found by a live write test against the API, not by reading
  the docs.
- **The OpenAPI spec misdescribes `POST /v1/apps`.** It declares
  `CreateAppResponse{token}`; the API returns `{id, created_at}`. The plugin does
  not read that body, so nothing breaks, but do not trust the spec there.
- **`private_v6` is not a supported `addressType`.** The assignment listing never
  reports the requested type, so `Read` infers it from the address family, and a
  private 6PN address is indistinguishable from a public IPv6 one — it would
  drift forever. `shared_v4`, `v4` and `v6` all round-trip.
- Managed Postgres — 22 REST operations — is the largest unimplemented area and
  the obvious next step. See `docs/RESOURCES.md`.
