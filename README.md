# formae-plugin-fly

A [Formae](https://github.com/platform-engineering-labs/formae) plugin for
[Fly.io](https://fly.io). Manage apps, machines, volumes, secrets, certificates and IP
addresses as declarative infrastructure.

Namespace `FLY`. Requires formae **0.84.0** or newer.

That floor is the SDK's own, and it is real rather than asserted: the schema uses only
`FieldHint` features present in the 0.84.0 Pkl package (`createOnly`, `writeOnly`,
`requiredOnCreate`, `hasProviderDefault`, `updateMethod = "EntitySet"` with `indexField`,
`formae.Value`, `formae.Resolvable`), and `schema/pkl/PklProject` pins that version so
`make verify-schema` proves it on every run. A plugin declaring a higher
`minFormaeVersion` than the agent is silently *skipped* at load — worth knowing, because
several sibling plugins currently pin 0.89.0 and therefore do not load against the
latest published agent (0.88.1).

---

## Credentials

The plugin reads the API token from the environment, never from a forma or from target
config. Resolution order matches `flyctl` exactly, so a token that works with `fly` works
here:

1. `FLY_ACCESS_TOKEN`
2. `FLY_API_TOKEN`

```bash
export FLY_API_TOKEN=$(fly auth token)   # personal access token
export FLY_ORG=my-org                    # organization slug — the real one, NOT "personal"
```

`~/.fly/config.yml` is deliberately *not* read — the plugin runs inside the formae agent,
often in a container with no `$HOME/.fly`.

### Which token types work

| Token | Create `fly tokens …` | Works? |
|-------|----------------------|--------|
| Personal access token | `fly auth token` | Yes — full surface. Best for local development. |
| Org token | `fly tokens create org` | Yes, for everything in that org, including app create. Best for CI. |
| Org read-only | `fly tokens create readonly` | Read, List and discovery only. Every write fails 403 → `AccessDenied`. |
| App deploy token | `fly tokens deploy` | **No.** Scoped to one existing app; cannot create apps. |

---

## Target configuration

```pkl
new formae.Target {
  label = "fly-target"
  config = new fly.Config {
    org    = "my-org"   // required — the organization's real slug
    region = "fra"      // optional — default region for machines and volumes
    baseUrl = null      // optional — defaults to https://api.machines.dev
  }
}
```

`org` is required: listing apps (`GET /v1/apps?org_slug=`) and org-wide machine and
volume discovery both need it, and it cannot be derived from a token.

**Use the real slug, not `"personal"`.** Fly accepts `personal` as a write-only alias and
then reports the organization's real slug on read. Because `App.org` is `createOnly`,
formae would compare the desired `personal` against the actual slug, see drift on an
immutable field, and plan a replacement on every reconcile — forever. The plugin refuses
`org = "personal"` at create and tells you the slug to use instead. Find it with
`fly orgs list`, or:

```bash
curl -s -H "Authorization: Bearer $FLY_API_TOKEN" \
  https://api.machines.dev/v1/tokens/current | jq -r '.tokens[0].org_slug'
```

`region` is a default so a forma need not repeat it on every machine and volume; a
resource-level `region` wins. Region codes are the three-letter Fly codes; the
authoritative list is public and needs no auth:

```bash
curl -s https://api.machines.dev/v1/platform/regions | jq -r '.Regions[].code'
```

`FLY_REGION` is *not* used as a fallback. Inside a Fly VM it means "the region I am
running in", which is a different thing from "the region to create in" — inheriting it
would make applies behave differently depending on where the agent runs.

---

## Supported resources

### Apps

| Resource type | CRUD | Notes |
|---------------|------|-------|
| `FLY::Apps::App` | C R D L | Free. Every field is `createOnly`: the API has no app-update endpoint. |
| `FLY::Apps::Machine` | C R U D L | **Bills per second while running.** Create and update are async. |
| `FLY::Apps::Volume` | C R U D L | Update covers backup settings and growth. Cannot shrink. |
| `FLY::Apps::Secrets` | C R U D L | One resource per app, holding the whole bag. Values are write-only. |
| `FLY::Apps::Certificate` | C R D L | ACME for a custom hostname. Create does not wait for DNS validation. |
| `FLY::Apps::IPAddress` | C R D L | Required for the app to be reachable at all. `shared_v4` and `v6` are free. |
| `FLY::Apps::VolumeSnapshot` | C R D\* L | Explicit snapshot at apply time. **Cannot be deleted** — see below. |
| `FLY::Apps::SecretKey` | C R U D L | App-scoped KMS key material. Not env-var secrets — that is `Secrets`. |

### Managed Postgres

| Resource type | CRUD | Notes |
|---------------|------|-------|
| `FLY::Postgres::Cluster` | C R D L | **A real always-on database, no free tier.** Create is async and slow. |
| `FLY::Postgres::Database` | C R D L | |
| `FLY::Postgres::User` | C R U D L | `role` is the only mutable field in the whole Postgres surface. |
| `FLY::Postgres::Attachment` | C R D L | Attaching injects a `DATABASE_URL` secret into the app — don't also declare it. |
| `FLY::Postgres::Extension` | C R D L | "Exists" means installed; the API lists the whole catalogue. |
| `FLY::Postgres::Backup` | C R D\* L | Backup at apply time. **Cannot be deleted** — see below. |

**\* Two resources cannot be deleted.** Fly exposes no delete endpoint for
`VolumeSnapshot` or `Postgres::Backup`; both expire under a retention policy. Their
delete reports success and says so in the status message — failing would wedge every
`formae destroy` containing one. Both are server-id-assigned, so re-applying after a
destroy takes a *new* artifact rather than reconciling to the existing one.

Everything above is the Machines REST API (`api.machines.dev`) — one transport, no
GraphQL. That is the declarative REST surface exhausted; what remains unimplemented
(organizations, WireGuard peers, egress IPs, Upstash Redis, Tigris buckets, tokens) is
GraphQL-only. See [docs/RESOURCES.md](docs/RESOURCES.md) for the full API catalog, what is
coming next (Managed Postgres is the big one, 22 REST operations), and what is
GraphQL-only. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) records the decisions.

### Things worth knowing before you apply

- **An app with machines and services is still unreachable** until it has an
  `IPAddress`. `fly deploy` allocates one implicitly; the raw Machines API does not.
- **A running machine does not pick up a changed secret** until it restarts. The plugin
  does not restart machines for you — a service interruption should not be an invisible
  side effect of a secret update. Use `fly machine restart`, or change something in the
  machine's config to force a new version.
- **`Machine.state` is an output, not desired state.** Setting it does nothing. fly-proxy
  stops and starts machines itself when `autostop`/`autostart` are set, so reconciling
  `state` would fight the platform in a loop that also bills for every wake-up. Use
  `fly machine start|stop` for runtime control.
- **`region` is `createOnly` on machines and volumes.** Fly has no move operation for
  either, so a region change is a replacement — and for a volume that means the data is
  gone.
- **A certificate can sit at `pending_validation` forever.** Create succeeds as soon as
  Fly accepts the request; publish the records in the resource's `dnsRequirements` output
  and then `fly certs check <hostname>`.

---

## Example

```pkl
amends "@formae/forma.pkl"

import "@formae/formae.pkl"
import "@fly/core/fly.pkl"

local flyOrg = read("env:FLY_ORG")

forma {
  new formae.Stack { label = "fly-demo" }

  new formae.Target {
    label = "fly-target"
    config = new fly.Config {
      org = flyOrg
      region = "fra"
    }
  }

  local app = new fly.App {
    label = "demo-app"
    name = "my-globally-unique-app-name"
    org = flyOrg
  }
  app

  new fly.Secrets {
    label = "demo-secrets"
    appName = app.res.name
    values {
      // Per-entry opacity: hashed at rest, never printed.
      ["DATABASE_URL"] = formae.value(read("env:DATABASE_URL")).opaque
    }
  }

  new fly.Machine {
    label = "demo-machine"
    appName = app.res.name
    name = "web"
    image = "flyio/hellofly:latest"
    guest = new fly.MachineGuest { cpuKind = "shared"; cpus = 1; memoryMb = 256 }
    services {
      new fly.MachineService {
        internalPort = 8080
        autostart = true
        autostop = "stop"
        ports {
          new fly.MachinePort { port = 443; handlers { "tls"; "http" } }
        }
      }
    }
  }

  new fly.IPAddress {
    label = "demo-ipv4"
    appName = app.res.name
    addressType = "shared_v4"
  }
}
```

```bash
formae apply --mode reconcile --watch main.pkl
formae destroy main.pkl
```

Runnable versions:

- [`examples/basic/`](examples/basic/) — one reachable Fly app: app, secret, machine, IPs.
- [`examples/fullstack-fly-supabase-vercel/`](examples/fullstack-fly-supabase-vercel/) —
  a full-stack app across three plugins, with a two-plugin variant that applies today.

### Secrets and opacity

`Secrets.values` is `writeOnly`, so formae never diffs the values — a changed value
cannot be detected as drift, only an added or removed *name*. The plugin also never asks
Fly to reveal values (the API can, via `?show_secrets=true`), so nothing pulls secret
material back out.

Mark individual entries opaque with `formae.value(x).opaque`; they are then hashed at
rest. A field-level `opaque` hint is not available for a map-valued field on formae
0.89.0 — see docs/ARCHITECTURE.md for why.

---

## Development

```bash
make build          # build to bin/fly
make lint           # golangci-lint
make test-unit      # unit tests (//go:build unit), no credentials needed
make verify-schema  # validate the Pkl schema
make install        # build + install to ~/.pel/formae/plugins/fly/v<version>/
```

Conformance tests run the **installed** binary, so `make install` is not optional —
`make conformance-test` does it for you as a dependency:

```bash
export FLY_API_TOKEN=$(fly auth token)
export FLY_ORG=my-org
export FLY_TEST_REGION=fra        # optional, defaults to fra

make conformance-test TEST=app                    # free, seconds
make conformance-test TEST=secrets                # free, seconds
make conformance-test TEST=machine TIMEOUT=15     # BILLABLE, async, needs the timeout
make conformance-test TEST=postgres TIMEOUT=30    # MOST EXPENSIVE: a real database
make conformance-test TIMEOUT=30                  # everything
```

`TIMEOUT` is in **minutes**, matching the documented convention. The Makefile appends the
`m` for `go test -timeout` and also exports it as `FORMAE_TEST_TIMEOUT`, which the harness
reads (in minutes) for its per-command polling deadline — so `TIMEOUT=15m` would be
rejected as `-timeout 15mm`. `VERSION=0.88.1` pins the agent version the harness downloads;
leaving it unset takes the latest published release.

The harness rewrites `schema/pkl/PklProject` and `testdata/PklProject` to the agent's
formae version for the duration of a run, then restores them. That is the compatibility
check doing its job, not a stray edit.

`make clean-environment` deletes every app in `FLY_ORG` whose name starts with
`formae-sdk-test-`. Destroying an app cascades to its machines, volumes, secrets,
certificates and IP assignments, so that one call is enough to stop a leaked machine
billing. It runs automatically before and after each conformance run, and needs `jq`.

Adding a resource is documented at the end of
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). The short version: read the operation out
of the OpenAPI spec (`https://docs.machines.dev/spec/openapi3.json`) rather than from a
Terraform provider, write the failing unit test first, and register only the operations
the API actually supports.

---

## License

FSL-1.1-ALv2. See [LICENSE](LICENSE).
