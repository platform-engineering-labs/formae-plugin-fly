# Fly.io Plugin for Formae

[![CI](https://github.com/platform-engineering-labs/formae-plugin-fly/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/platform-engineering-labs/formae-plugin-fly/actions/workflows/ci.yml)

Fly.io resource plugin for
[formae](https://github.com/platform-engineering-labs/formae). This plugin
enables Formae to manage Fly.io resources using the [Machines REST
API](https://docs.machines.dev/) — apps, machines, volumes, secrets,
certificates, IP addresses and Managed Postgres.

Requires formae **0.89.0** or newer — the schema uses `formae.ValueSource`, the
0.89.0 secret/generator binding type.

## Supported Resources

This plugin supports **14 Fly.io resource types** across 2 services. See
[`schema/pkl/`](schema/pkl/) for field definitions — every class carries the API
behaviour it was written against, including what is deliberately not
implemented.

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
- **`Secrets.values` is write-only.** It is never echoed back, so value drift is
  still not detected — only an added or removed name. Wrap sensitive entries in
  `formae.value(x).opaque` to have them hashed at rest: opacity is per entry
  here, because formae derives it from a field's declared type and does not
  descend into map value positions. `SecretKey.value` and the bag's read-only
  `decodedValues` are typed opaque and hashed at rest whatever you write.
- **`FLY::Apps::Secrets` is a `formae.Secret`.** Read reveals the bag
  (`GET /v1/apps/{app}/secrets?show_secrets=true`) onto a read-only
  `decodedValues` field, so an entry is referenceable as
  `bag.res.secretValue.at("KEY")` — including the `DATABASE_URL` a Postgres
  attachment injects, which formae never wrote. Reveal happens on **Read only**;
  discovery lists names. A token that may list but not reveal still reads the
  bag, just without the values.

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

`org` is required: listing apps (`GET /v1/apps?org_slug=`) and org-wide volume
discovery both need it, and it cannot be derived from a token. Machine discovery
goes through the app list too — see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
for why it does not use Fly's org-wide machine index.

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

### Secrets

`FLY::Apps::Secrets` entries and `FLY::Apps::SecretKey.value` take
`formae.ValueSource`, so a credential need never be written into a forma. (On
`SecretKey.value` a generator draw type-checks but will not be valid key
material — Fly wants base64 sized for the `keyType`. Omit the field and let Fly
generate the key.)

Let formae draw it. Without a `rotation` the value is drawn once and never
changes — the replacement for minting a password at eval time and pinning it
with `setOnce`. With one, formae rotates on the cadence and moves every
destination bound to the generator together:

```pkl
local sessionPw = new formae.PasswordGenerator {
  label = "api-session-secret"
  stack = appStack.res
  rotation = new formae.RotationSpec { every = 30.d }
}

local apiSecrets = new fly.Secrets {
  label = "api-secrets"
  appName = api.res.name
  values {
    ["SESSION_SECRET"] = sessionPw.gen.value
  }
}
```

Fly reads secrets back, too. `FLY::Apps::Secrets` is a `formae.Secret` whose
value is the bag, so an entry is reachable by key — including one Fly wrote
itself:

```pkl
local pg = new fly.Attachment {
  label = "api-db"
  clusterId = cluster.res.id
  appName = api.res.name          // Fly injects DATABASE_URL into this app
}

local apiSecrets = new fly.Secrets {
  label = "api-secrets"
  appName = api.res.name
  values { ["SESSION_SECRET"] = sessionPw.gen.value }
}

// Somewhere else entirely — another plugin, another stack:
//   dsn = apiSecrets.res.secretValue.at("DATABASE_URL")
```

The bag does not declare `DATABASE_URL`, and must not: Fly owns that key and the
two would fight over it. Reading it back is fine — `values` is the write side,
`decodedValues` the read side, and they are separate fields.

`secretValue` on a map-shaped secret is an accessor, not a value: `.at(key)` is
required and a bare `secretValue` will not type-check.

Or hand Fly a secret another provider holds. It is read live on every plugin
call, so rotating it upstream takes effect without re-applying here:

```pkl
values {
  ["DB_PASSWORD"] = dbSecret.res.secretValue          // scalar secret
  ["API_KEY"]     = vaultSecret.res.secretValue.at("api-key")  // map-shaped
  ["TOKEN"]       = appSecret.res.secretValue.json("creds.token")
}
```

A reference is a handle, not a string: pass it whole, never interpolated.

Rotation inherits the Fly caveat noted above: a running machine does not pick up
the new value until it restarts, and formae will not restart it for you.

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
[CONTRIBUTING.md](CONTRIBUTING.md). Design decisions live next to the code they
explain, in `schema/pkl/core/fly.pkl` and the provisioners under `pkg/`.

## License

This plugin is licensed under the [Functional Source License, Version 1.1, ALv2
Future License (FSL-1.1-ALv2)](LICENSE).

Copyright 2026 Platform Engineering Labs Inc.
