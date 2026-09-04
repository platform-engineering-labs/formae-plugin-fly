# Changelog

All notable changes to `formae-plugin-fly`.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versioning follows [semver](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — unreleased

First release. Requires formae 0.84.0 or newer.

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
- Discovery for all six types. Machine and Volume use the org-wide endpoints
  (one cursor-paged call); Secrets, Certificate and IPAddress fan out per app
  because no org-wide endpoint exists.
- Conformance coverage — CRUD lifecycle and discovery — for App, Secrets,
  Machine, Volume, IPAddress and the whole Managed Postgres graph (cluster,
  database, user, extension, attachment) in one forma, so a single billable
  cluster serves every child. Ten of the fourteen types are exercised against
  the live API.

  Not conformance-tested, deliberately: `Certificate` never converges without
  DNS records on a domain the suite controls, and `VolumeSnapshot` /
  `Postgres::Backup` have no delete endpoint, so every run would leak an
  artifact. `SecretKey` is unit-tested only. Reasoning in docs/RESOURCES.md.
- `examples/basic/` — one publicly reachable Fly app.
- `examples/fullstack-fly-supabase-vercel/` — a three-tier application across
  Fly, Supabase and Vercel wired with cross-plugin resolvables, plus a
  two-provider variant.
- `docs/RESOURCES.md` and `docs/ARCHITECTURE.md` — the API catalog, the counts
  behind it, and the design decisions.

### Notes and known limitations

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
  added or removed name. The plugin never asks Fly to reveal values. Per-entry
  opacity is available via `formae.value(x).opaque`; a field-level `opaque` hint
  is not expressible for a map-valued field on formae 0.89.0.
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
