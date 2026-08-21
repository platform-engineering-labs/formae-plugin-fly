# Full-stack: Fly.io + Supabase + Vercel

A three-tier application declared once, across three providers, in one `formae apply`.

| Tier | Provider | Resources |
|------|----------|-----------|
| Data | Supabase | `Platform::Project` (Postgres + auth), two `Auth::APIKey`s |
| Backend | Fly.io | `Apps::App`, `Apps::Secrets`, `Apps::Machine`, two `Apps::IPAddress` |
| Frontend | Vercel | `Projects::Project`, three `Projects::EnvironmentVariable`s |

Two formae in this directory:

| File | Providers | Needs |
|------|-----------|-------|
| [`main.pkl`](main.pkl) | all three | Fly + Supabase + Vercel credentials |
| [`fly-supabase.pkl`](fly-supabase.pkl) | Fly + Supabase | no Vercel account |

`fly-supabase.pkl` is the same Supabase-to-Fly wiring with the frontend tier removed —
useful if you only have two of the three accounts, or to see the cross-plugin mechanism
without the Vercel noise.

---

## The wiring is the point

```
Supabase project ──┬──> secret key ──────> Fly secret ──> Fly machine
                   ├──> publishable key ────────────────> Vercel env var
                   └──> project ref ───────┬────────────> Fly machine env
                                           └────────────> Vercel env var
Fly app ─────────────────────────────────────────────────> Vercel env var
```

Every arrow is a **resolvable** — `db.res.id`, `secretKey.res.apiKey`, `api.res.name`.
A resolvable is a handle, not a value: formae builds the dependency graph from it and
substitutes the real value at apply time. Nobody copies a key out of one dashboard into
another, and no credential appears in the forma or in the state formae keeps for the
consuming provider.

Three consequences worth understanding before you copy this pattern:

**A resolvable cannot be interpolated into a string.** So this forma passes
*identifiers* across provider boundaries and lets each service compose its own URLs at
runtime — the backend builds its Postgres DSN from `SUPABASE_PROJECT_REF` and
`SUPABASE_DB_PASSWORD`, the frontend builds the API URL from `NEXT_PUBLIC_API_APP` as
`https://${NEXT_PUBLIC_API_APP}.fly.dev`. That is also the more robust shape: a hostname
assembled at deploy time cannot go stale.

**The Postgres password comes from an environment variable, not from
`random.password(...).setOnce`.** It has to be *the same value* in two places — Supabase's
`dbPass` and the Fly secret the backend reads — and `setOnce` is scoped per resource
field, so two `setOnce` expressions would generate two different passwords and the
backend would silently fail to connect. One env var makes them provably equal. `.opaque`
still applies at both ends, so the value is hashed at rest and never printed.

**The machine references the Secrets bag, not the app.** Fly injects secrets into a
machine's environment *at boot*; a machine created before its secrets exist boots without
them and stays that way until it restarts. `appName = apiSecrets.res.appName` resolves to
the same app name but forces the order App → Secrets → Machine.

And one rule that is not about mechanism: the **publishable** Supabase key goes to Vercel,
the **secret** key goes to Fly. A `NEXT_PUBLIC_` variable is exposed to the client bundle,
so a secret key in one is a secret key on the internet.

---

## Required environment variables

| Variable | Required | Used for |
|----------|----------|----------|
| `FLY_API_TOKEN` *or* `FLY_ACCESS_TOKEN` | yes | Fly plugin auth. `fly auth token`. `FLY_ACCESS_TOKEN` wins if both are set. |
| `FLY_ORG` | yes | Fly organization slug, or `personal` |
| `SUPABASE_ACCESS_TOKEN` | yes | Supabase plugin auth. [supabase.com/dashboard/account/tokens](https://supabase.com/dashboard/account/tokens) |
| `SUPABASE_ORGANIZATION_ID` | yes | Owns the project. [supabase.com/dashboard/org/_/general](https://supabase.com/dashboard/org/_/general) |
| `SUPABASE_DB_PASS` | yes | Postgres password, ≥ 8 chars. Never written to the forma. |
| `VERCEL_TOKEN` | `main.pkl` only | Vercel plugin auth. [vercel.com/account/settings/tokens](https://vercel.com/account/settings/tokens) |
| `APP_SLUG` | recommended | Base name for everything. **Fly app names are unique across all of Fly.io**, so the default will already be taken. |
| `API_IMAGE` | no | Backend container image. Defaults to `flyio/hellofly:latest`. |
| `FLY_REGION_OVERRIDE` | no | Fly region. Defaults to `fra`. Named this rather than `FLY_REGION` because inside a Fly VM that variable means "the region I am running in". |
| `SUPABASE_REGION` | no | Supabase region. Defaults to `eu-central-1`. |

```bash
export FLY_API_TOKEN=$(fly auth token)
export FLY_ORG=personal
export SUPABASE_ACCESS_TOKEN=sbp_...
export SUPABASE_ORGANIZATION_ID=...
export SUPABASE_DB_PASS="$(openssl rand -base64 24 | tr -d '/+=')"
export VERCEL_TOKEN=...
export APP_SLUG=my-fullstack-demo
```

Keep `SUPABASE_DB_PASS` — the same value must be exported on every later apply, or
Supabase and Fly will disagree about the password.

---

## Install the plugins

All three schemas are referenced from local checkouts, so the three repositories must sit
side by side:

```
plugins/
├── formae-plugin-fly        <- this example lives here
├── formae-plugin-supabase
└── formae-plugin-vercel
```

Install order does not matter — formae resolves plugins by namespace at apply time — but
all three must be installed before the apply:

```bash
cd ../../../formae-plugin-supabase && make install
cd ../formae-plugin-vercel         && make install
cd ../formae-plugin-fly            && make install

formae plugin list    # expect fly, supabase and vercel
```

---

## Apply

```bash
cd formae-plugin-fly

# All three tiers
formae apply --mode reconcile --watch examples/fullstack-fly-supabase-vercel/main.pkl

# Fly + Supabase only
formae apply --mode reconcile --watch examples/fullstack-fly-supabase-vercel/fly-supabase.pkl
```

`--watch` matters here: two resources are genuinely async. The Supabase project takes
about 2–3 minutes to reach `ACTIVE_HEALTHY`, and the Fly machine another 5–90 seconds to
pull its image and boot. Without `--watch` the command returns while both are still
converging.

Preview without touching anything:

```bash
formae apply --simulate examples/fullstack-fly-supabase-vercel/main.pkl
```

## Destroy

```bash
formae destroy examples/fullstack-fly-supabase-vercel/main.pkl
```

Destroy the Fly side first if you are cleaning up by hand — a machine left running keeps
billing. Deleting a Fly *app* cascades to its machines, volumes, secrets and IP
assignments, so `fly apps destroy $APP_SLUG-api` is the one-command fallback.

---

## What it looks like when it works

```
$ formae inventory --stack fullstack-fly-supabase-vercel

SUPABASE::Platform::Project      app-database              ACTIVE_HEALTHY
SUPABASE::Auth::APIKey           frontend-publishable-key  sb_publishable_...
SUPABASE::Auth::APIKey           backend-secret-key        sb_secret_...
FLY::Apps::App                   api-app                   deployed
FLY::Apps::Secrets               api-secrets               my-slug-api
FLY::Apps::Machine               api-machine               started
FLY::Apps::IPAddress             api-ipv4                  66.241.x.x
FLY::Apps::IPAddress             api-ipv6                  2a09:8280:...
VERCEL::Projects::Project        app-frontend              prj_...
VERCEL::Projects::EnvironmentVariable  api-app-name        NEXT_PUBLIC_API_APP
VERCEL::Projects::EnvironmentVariable  supabase-project-ref
VERCEL::Projects::EnvironmentVariable  supabase-anon-key
```

Then, end to end:

```bash
# The Fly API is reachable and running your image
curl -s https://$APP_SLUG-api.fly.dev/

# It received the Supabase wiring — the project ref as an env var,
# the password and secret key as Fly secrets (names only; values never readable)
fly secrets list -a $APP_SLUG-api
# NAME                    DIGEST      CREATED AT
# SUPABASE_DB_PASSWORD    xxxxxxxx    1m ago
# SUPABASE_SECRET_KEY     xxxxxxxx    1m ago

# The Vercel project has the frontend's three variables
vercel env ls --scope <team> --token $VERCEL_TOKEN
```

The Vercel project has no deployment until you push code to it or connect a Git
repository (uncomment `gitRepository` in `main.pkl`) — formae creates the project and its
configuration, not a build. Until then the frontend tier exists and is configured but
serves nothing, which is the correct state for infrastructure-as-code to leave it in.

The backend image is `flyio/hellofly:latest` by default: it serves HTTP on 8080 and
proves the machine boots and is routable, but it ignores the Supabase variables. Point
`API_IMAGE` at your own API to make the data path real; the environment it will find is:

| Variable | Source | Kind |
|----------|--------|------|
| `SUPABASE_PROJECT_REF` | machine `env` | plain — a project ref is a public identifier |
| `PORT` | machine `env` | plain |
| `SUPABASE_DB_PASSWORD` | Fly secret | opaque |
| `SUPABASE_SECRET_KEY` | Fly secret | opaque, from the Supabase plugin |

```
postgresql://postgres:$SUPABASE_DB_PASSWORD@db.$SUPABASE_PROJECT_REF.supabase.co:5432/postgres
```

---

## Costs

| | |
|---|---|
| Supabase project | A real Postgres database. Free plan allows two per organization; `plan = "free"` keeps it there. |
| Fly machine | **Bills per second while running.** This uses the cheapest guest Fly offers — `shared-cpu-1x`, 256 MB. `autostop = "stop"` means fly-proxy stops it when idle, so an API with no traffic costs nothing. |
| Fly IP addresses | `shared_v4` and `v6` are free. A dedicated `v4` is billable — this example does not allocate one. |
| Vercel project | Free on Hobby. |

---

## Troubleshooting

**`422` on the Fly app create** — the app name is taken. Fly app names are globally
unique; change `APP_SLUG`.

**Machine stuck `InProgress`** — it is pulling its image. A large image on a cold host can
take well over a minute. `fly logs -a $APP_SLUG-api` shows the pull.

**Machine reached `started`, then went `stopped`** — that is `autostop` doing its job, not
a failure. The plugin reports `stopped` as a settled state precisely so it does not fight
fly-proxy.

**Backend cannot connect to Postgres** — check that `SUPABASE_DB_PASS` is the same value
you exported on the first apply. If it changed, Supabase still has the original (`dbPass`
is `createOnly`) while the Fly secret has the new one.

**Vercel `custom_domain_needs_upgrade`** — a Hobby project cannot take custom domains.
This example does not add one; if you extended it with `vercel.Domain`, that is why.
