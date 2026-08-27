# Changelog

All notable changes to `formae-plugin-fly`.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versioning follows [semver](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — unreleased

First release. Requires formae 0.84.0 or newer.

### Added

Six resource types, all on the Fly.io Machines REST API (`api.machines.dev`):

| Resource | Operations |
|----------|-----------|
| `FLY::Apps::App` | Create, Read, Delete, List |
| `FLY::Apps::Machine` | Create, Read, Update, Delete, Status, List |
| `FLY::Apps::Volume` | Create, Read, Update, Delete, List |
| `FLY::Apps::Secrets` | Create, Read, Update, Delete, List |
| `FLY::Apps::Certificate` | Create, Read, Delete, List |
| `FLY::Apps::IPAddress` | Create, Read, Delete, List |

App, Certificate and IPAddress expose no Update: the API has no update endpoint
for any of them, so every field is `createOnly` and a change is a replacement.

- Credential resolution matching flyctl exactly: `FLY_ACCESS_TOKEN`, then
  `FLY_API_TOKEN`.
- Discovery for all six types. Machine and Volume use the org-wide endpoints
  (one cursor-paged call); Secrets, Certificate and IPAddress fan out per app
  because no org-wide endpoint exists.
- Conformance coverage for App, Secrets and Machine.
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
- **`private_v6` is not a supported `addressType`.** The assignment listing never
  reports the requested type, so `Read` infers it from the address family, and a
  private 6PN address is indistinguishable from a public IPv6 one — it would
  drift forever. `shared_v4`, `v4` and `v6` all round-trip.
- Managed Postgres — 22 REST operations — is the largest unimplemented area and
  the obvious next step. See `docs/RESOURCES.md`.
