# Fly.io Plugin for Formae

[![CI](https://github.com/platform-engineering-labs/formae-plugin-fly/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/platform-engineering-labs/formae-plugin-fly/actions/workflows/ci.yml)

Fly.io resource plugin for
[formae](https://github.com/platform-engineering-labs/formae). This plugin
enables Formae to manage Fly.io resources using the [Machines REST
API](https://docs.machines.dev/) — apps, machines, volumes, secrets,
certificates, IP addresses and Managed Postgres.

Requires formae **0.84.0** or newer.

## Supported Resources

This plugin supports **14 Fly.io resource types** across 2 services. See
[`schema/pkl/`](schema/pkl/) for field definitions and
[`docs/RESOURCES.md`](docs/RESOURCES.md) for the full API catalog, including
what is not yet implemented and why.

| Resource Type | Description |
|---------------|-------------|
| `FLY::Apps::App` | Fly app — the namespace every other resource lives in. Free until a machine runs |
| `FLY::Apps::Machine` | One Firecracker VM running one container image. **Bills per second while running** |
| `FLY::Apps::Volume` | Persistent volume attached to at most one machine |
| `FLY::Apps::VolumeSnapshot` | Point-in-time snapshot of a volume |
| `FLY::Apps::Secrets` | All of an app's secrets as one resource, injected into machines at boot |
| `FLY::Apps::SecretKey` | App-scoped KMS key material for the encrypt / sign endpoints |
| `FLY::Apps::Certificate` | ACME certificate for a custom hostname |
| `FLY::Apps::IPAddress` | IP address assigned to an app. Required for the app to be reachable |
| `FLY::Postgres::Cluster` | Managed Postgres cluster. **A real always-on database, no free tier** |
| `FLY::Postgres::Database` | Database inside a Managed Postgres cluster |
| `FLY::Postgres::User` | Role inside a cluster. `role` is the only mutable field |
| `FLY::Postgres::Attachment` | Attaches a Fly app to a cluster, injecting a `DATABASE_URL` secret |
| `FLY::Postgres::Extension` | Postgres extension enabled in one database |
| `FLY::Postgres::Backup` | Backup of a Managed Postgres cluster |

Everything is the Machines REST API on one transport — no GraphQL client. That
is the declarative REST surface exhausted; what remains (organizations,
WireGuard peers, egress IPs, Upstash Redis, Tigris buckets, tokens) is
GraphQL-only.

### Notes

Behaviour that will surprise you otherwise:

- **Use the organization's real slug, not `"personal"`.** Fly accepts `personal`
  on create and reports the real slug on read; since `org` is `createOnly` that
  is permanent drift. The plugin refuses it and names the slug to use.
- **An app is unreachable from the internet until it has an `IPAddress`.**
  `fly deploy` allocates one implicitly; the raw Machines API does not.
  `shared_v4` and `v6` are free, a dedicated `v4` is billable.
- **A running machine does not pick up a changed secret until it restarts.** The
  plugin does not restart machines for you — a service interruption should not
  be an invisible side effect of a secret update.
- **`Machine.state` is an output, not desired state.** Setting it does nothing:
  fly-proxy stops and starts machines itself under `autostop`/`autostart`, so
  reconciling it would fight the platform.
- **`region` is `createOnly` on machines and volumes.** Fly has no move
  operation, so a region change replaces the resource — and for a volume that
  means the data is gone.
- **`VolumeSnapshot` and `Postgres::Backup` cannot be deleted.** Fly exposes no
  delete endpoint; both expire under a retention policy. Their delete reports
  success and says so.
- **`Secrets.values` is write-only.** Value drift cannot be detected, only an
  added or removed name. Wrap sensitive entries in `formae.value(x).opaque` to
  have them hashed at rest.

## Configuration

### Target Configuration

Configure a Fly.io target in your Forma file:

```pkl
import "@formae/formae.pkl"
import "@fly/core/fly.pkl"

target: formae.Target = new formae.Target {
  label = "fly-target"
  config = new fly.Config {
    org = "my-org"      // required — the organization's real slug
    region = "fra"      // optional — default region for machines and volumes
    // Optional: override the API endpoint
    // baseUrl = "http://_api.internal:4280"
  }
}
```

`org` is required: listing apps (`GET /v1/apps?org_slug=`) and org-wide machine
and volume discovery both need it, and it cannot be derived from a token.

`region` is a default so a forma need not repeat it on every machine and volume;
a resource-level `region` wins. The authoritative region list is public and
needs no auth:

```bash
curl -s https://api.machines.dev/v1/platform/regions | jq -r '.Regions[].code'
```

`FLY_REGION` is deliberately not used as a fallback — inside a Fly VM it means
"the region I am running in", which would make applies behave differently
depending on where the agent runs.

### Credentials

The plugin reads the API token from the environment, never from a forma or from
target config. Resolution order matches `flyctl` exactly, so a token that works
with `fly` works here:

```bash
export FLY_ACCESS_TOKEN="your-token"   # checked first
export FLY_API_TOKEN="your-token"      # fallback
export FLY_ORG="my-org"                # organization slug
```

`~/.fly/config.yml` is deliberately not read — the plugin runs inside the formae
agent, often in a container with no `$HOME/.fly`.

**Which token types work:**

| Token | Create with | Works? |
|-------|-------------|--------|
| Personal access token | `fly auth token` | Yes — full surface. Short-lived; best for local development |
| Org token | `fly tokens create org` | Yes, for everything in that org, including app create. Best for CI |
| Org read-only | `fly tokens create readonly` | Read, List and discovery only. Writes fail 403 |
| App deploy token | `fly tokens deploy` | **No** — scoped to one existing app, cannot create apps |

`GET /v1/tokens/current` reports what a token can do:

```bash
curl -s -H "Authorization: Bearer $FLY_API_TOKEN" \
  https://api.machines.dev/v1/tokens/current | jq
```

## Examples

See the [examples/](examples/) directory for usage examples.

```bash
# Evaluate an example
formae eval examples/basic/main.pkl

# Apply resources — returns a command id; poll it to watch progress
formae apply --mode reconcile --yes examples/basic/main.pkl
formae command status <id> --output-layout detailed
```

| Example | Shows | Costs anything? |
|---------|-------|-----------------|
| [`basic/`](examples/basic/) | One publicly reachable Fly app: app, secret, machine, IPv4 + IPv6 | Yes — the machine bills per second |
| [`fullstack-fly-supabase-vercel/`](examples/fullstack-fly-supabase-vercel/) | A three-tier app across Fly, Supabase and Vercel, wired with cross-plugin resolvables. Includes a two-provider variant | Yes — a Supabase project and a Fly machine |

There is no `--watch` flag in formae 0.89.0: `formae apply` returns as soon as
the agent accepts the command. Machine creates, and Managed Postgres creates in
particular, are asynchronous — follow them with `formae command status`.

Contributor setup, conformance testing and publishing are in
[CONTRIBUTING.md](CONTRIBUTING.md). Design decisions and the API research behind
them are in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## License

This plugin is licensed under the [Functional Source License, Version 1.1, ALv2
Future License (FSL-1.1-ALv2)](LICENSE).

Copyright 2026 Platform Engineering Labs Inc.
