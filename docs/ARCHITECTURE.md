# formae-plugin-fly — Architecture

Companion to [RESOURCES.md](./RESOURCES.md), which carries the API counts and the
resource catalog. This document records the decisions.

---

## Namespace: `FLY`, not `FLYIO`

Kept as scaffolded. Every identifier in the Fly.io ecosystem uses the short form:
the CLI binary is `fly`, the app manifest is `fly.toml`, the credential env vars are
`FLY_API_TOKEN` / `FLY_ACCESS_TOKEN` / `FLY_ORG` / `FLY_REGION`, and nine of the ten
community Terraform providers are registered as `<owner>/fly` with a `fly_` resource
prefix. `FLYIO` would be the only place in a user's stack spelling it that way.
`FLY::Apps::Machine` also reads better than `FLYIO::Apps::Machine` in a forma.

No change to `formae-plugin.pkl`.

---

## Transport: one REST client, no GraphQL

**Decision: a single HTTP client against `https://api.machines.dev`. No GraphQL client
in v1.**

This contradicts the assumption the plugin was scoped under — that certificates and IP
addresses would force a second, GraphQL transport. That assumption was true in 2023 and
is why `andrewbaxter/terraform-provider-fly` ships both a `machineapi/` and a `graphql/`
package. It is no longer true. Verified 2026-08-21 by unauthenticated route probe
(HTTP 401 means the route exists and rejected us for lack of a token; HTTP 404 with
`404 page not found` means no such route):

```
GET /v1/apps/{app}/certificates    → 401 {"message":"Unauthorized"}
GET /v1/apps/{app}/ip_assignments  → 401 {"message":"Unauthorized"}
GET /v1/apps/{app}/secrets         → 401 {"error":"Authenticate: token validation error"}
GET /v1/apps/{app}/volumes         → 401 {"error":"Authenticate: token validation error"}
GET /v1/apps/{app}/machines        → 401 {"error":"Authenticate: token validation error"}
GET /v1/postgres                   → 404 {"error":"Organization not found"}   # route exists, org lookup failed
GET /v1/platform/regions           → 200 {"Regions":[...]}                    # public
GET /v1/bogus_route_check          → 404 404 page not found                   # control: no such route
```

All six P1 resources are REST. Managed Postgres (P2, 22 operations) is REST. The GraphQL
API stays live — 396 types, 93 mutations — but the only things it uniquely offers
(organizations, WireGuard peers, egress IPs, Upstash/Tigris add-ons, legacy scaling) are
all P3. Adding a GraphQL client for zero P1 and zero P2 resources would be carrying a
second transport, a second error taxonomy, and a second auth path for nothing.

**When a second transport becomes necessary** — someone implements
`FLY::Platform::Organization` or `FLY::Addons::Redis` — the shape is a sibling package
`pkg/transport/flygql` with its own `Do(ctx, query, vars, out)` and its own
`ClassifyError`. GraphQL returns HTTP 200 with an `errors[]` array, so the
status-code-driven classification in `pkg/transport/fly` cannot be reused as-is; that is
the real cost, and it is why the split is drawn at the transport and not inside one
client. `Plugin.getDeps` already returns a dependency bundle, so a second client is an
extra field there and a wider `registry.Factory` signature — not a rewrite.

Two operational notes about the one transport we do have:

- **`api.machines.dev` is a facade over more than one upstream.** The 401 bodies above
  come in two different shapes — `{"message":...}` from the apps/certificates/IP
  service, `{"error":...}` from flaps (the machines/volumes/secrets service). The error
  decoder probes `message`, then `error`, then `msg`; do not assume one field.
- **`GET /v1/apps` requires `org_slug`** as a query parameter (the spec marks it
  required; unauthenticated it 404s rather than 401s). App listing is therefore always
  org-scoped, which is why `org` is a target-config field and not optional.

---

## Authentication and configuration

### Credential resolution order

Matches `flyctl` exactly, so a token that works with `fly` works here.
`superfly/flyctl` `internal/config/config.go:150` does
`env.First(AccessTokenEnvKey, APITokenEnvKey)` where those are `FLY_ACCESS_TOKEN` and
`FLY_API_TOKEN` — access token first, API token as fallback. (The Machines API docs
mention only `FLY_API_TOKEN`; flyctl accepts both, access token winning.)

1. `FLY_ACCESS_TOKEN`
2. `FLY_API_TOKEN`
3. → fail with `OperationErrorCodeInvalidCredentials`

Deliberately **not** implemented: reading `~/.fly/config.yml`'s `access_token` key, which
is flyctl's third source. The plugin runs inside the formae agent, frequently in a
container that has no `$HOME/.fly`, and a YAML dependency plus a file-permission story is
a lot of surface for a convenience that one `export` covers. Noted here so the omission
is a decision rather than an oversight.

Credentials never appear in a forma, in target config, or in state.

### Token types

Fly.io has app-scoped and org-scoped tokens (`fly tokens create deploy|ssh|machine-exec`,
`fly tokens create org|readonly`), plus the personal access token from
`fly auth token`.

| Token | Works for this plugin? |
|-------|------------------------|
| Personal access token (`fly auth token`) | Yes — full surface. What you want for local dev. |
| Org token (`fly tokens create org`) | Yes for everything inside that org, including app create. The right choice for CI. |
| Org read-only (`fly tokens create readonly`) | Read + List + discovery only. Every Create/Update/Delete fails 403 → `AccessDenied`. |
| App deploy token (`fly tokens deploy`) | **No.** Scoped to one existing app; cannot create apps. Machines/secrets/volumes on that one app only. |

`GET /v1/tokens/current` reports what a token can do. The plugin does not call it — a
failed operation reports 401/403 through the normal error path, which is a clearer signal
than a preflight check that can itself be blocked.

### Target config

```pkl
config = new fly.Config {
    org    = "my-org"        // required: Fly organization slug, or "personal"
    region = "fra"           // optional default region for machines/volumes
    baseUrl = null           // optional override, defaults to https://api.machines.dev
}
```

`org` is required because app create (`POST /v1/apps` body `org_slug`) and app list
(`GET /v1/apps?org_slug=`) both need it, and it is not derivable from a token.
`region` is a target-level default so a forma does not have to repeat it on every
Machine and Volume; a resource-level `region` overrides it.

### Regions

`region` is `createOnly` on Machine and Volume. Fly has no move operation for either — a
volume is physical storage in one host in one region, and a machine is pinned to the host
its rootfs lives on. Changing `region` therefore replaces the resource, which for a
Volume means the data is gone. That is the honest model; the alternative (silently
ignoring a region change) is worse.

Region codes are the three-letter Fly codes (`fra`, `iad`, `ams`, `sjc`, …). The
authoritative list is public and needs no auth: `GET /v1/platform/regions`.
`FLY_REGION` is *not* read as a fallback — it means "the region this machine is running
in" inside a Fly VM, which is a different thing from "the region to create in", and
inheriting it would make applies behave differently depending on where the agent runs.

---

## Native ID format

Everything except App is app-scoped, and Fly IDs are only unique within an app, so the
native ID carries the app name.

| Resource | Native ID | Notes |
|----------|-----------|-------|
| `FLY::Apps::App` | `{app_name}` | App names are globally unique across all of Fly.io |
| `FLY::Apps::Machine` | `{app_name}/{machine_id}` | e.g. `my-api/17811943c9d489` |
| `FLY::Apps::Volume` | `{app_name}/{volume_id}` | e.g. `my-api/vol_9x1kj4rz` |
| `FLY::Apps::Secrets` | `{app_name}/secrets` | one bag per app |
| `FLY::Apps::Certificate` | `{app_name}/{hostname}` | hostname *is* the server-side key |
| `FLY::Apps::IPAddress` | `{app_name}/{ip}` | the allocated address, assigned by Fly |

`Secrets` uses the literal suffix `/secrets` rather than the bare app name on purpose.
Formae keys inventory by `(target, nativeID)` *irrespective of resource type*, so a bare
app name would make the secrets bag a duplicate of its own App — and one of them would
get deleted. (This is a bug the Supabase plugin hit and fixed the same way; the comment
lives on in `pkg/resources/functions/secret.go` there.)

`POST /v1/apps` responds with `CreateAppResponse{token}` — **it does not echo the app.**
The native ID is the name we sent, not something read back. That is safe because app
names are caller-chosen and globally unique: a name collision fails the create with 422
rather than silently binding to someone else's app.

---

## Async operations and `Status()`

Fly's REST calls return promptly; what takes time is the resource reaching a usable
state. The rule applied: **return `InProgress` only where the resource is unusable until
it settles, and where it will settle without human action.**

| Operation | Sync or async | Why |
|-----------|---------------|-----|
| App create / delete | **Sync** | `POST /v1/apps` → 201 immediately; the app is a namespace, there is nothing to converge. Delete is immediate (rate-limited to 100/min). |
| Machine create | **Async** — `InProgress` + `RequestID` | Response returns `state: "created"`, then the machine pulls its image and boots. Real time to `started`: 5–90 s, longer for a cold image. |
| Machine update | **Async** | `POST /v1/apps/{app}/machines/{id}` replaces the machine's config and reboots it; the new version has to come up. |
| Machine delete | **Sync** | `DELETE ?force=true` returns once the machine is gone. |
| Volume create | **Sync** | Provisioning is fast and the volume is `created` in the response. If a future region turns out to be slow, this becomes async with no schema change. |
| Volume extend | **Sync** | `PUT .../extend` returns the new size. |
| Secrets set / delete | **Sync** | Writes to the app's secret store. Note: existing machines do **not** pick up a new secret until they restart — see below. |
| Certificate create | **Sync**, deliberately | See below. |
| IP allocate / release | **Sync** | Allocation is immediate. |

### Machine polling

`RequestID` is the machine's native ID (`{app}/{machine_id}`) — the same value formae
already has. There is no separate operation handle in the Machines API, so inventing one
would just be state to lose across a plugin restart.

`Status()` polls `GET /v1/apps/{app}/machines/{id}` and maps `state`:

| Machine `state` | `Status()` result |
|-----------------|-------------------|
| `created`, `starting`, `replacing` | `InProgress` |
| `started` | `Success` |
| `stopped`, `suspended` | `Success` — a machine that was asked to stop has arrived |
| `destroying`, `destroyed` | `Success` on a delete; `Failure` on a create |
| `failed` | `Failure`, `ServiceInternalError` |

The API also offers `GET .../wait?state=started&timeout=60`, a long-poll. Not used:
holding an HTTP request open for up to 60 s inside a `Create()` fights formae's own
watchdog (the reconciler expects plugin operations to return quickly and re-drives
`Status()` itself), and a long-poll that times out gives no more information than a cheap
`GET` does. One `GET` per poll, formae owns the cadence.

### Machine desired state (`started`/`stopped`) is out of scope for v1

The brief asked for an explicit decision. **Not modeled.** Machine lifecycle state is
readable (it is in `Read` output as `state`) but not reconciled: setting
`state = "stopped"` in a forma does nothing.

The reason is not effort — start/stop are two endpoints — it is that **Fly.io itself
mutates machine state as normal operation.** `services[].autostop` (`off`/`stop`/`suspend`)
and `autostart` mean fly-proxy stops idle machines and starts them on the next request;
that is the platform's headline feature and the default for anything with an HTTP service.
A reconciled `state` field would read a legitimately-autostopped machine as drift, start
it, get it autostopped again, and produce an infinite reconcile loop that also bills the
user for the machines it keeps waking. Formae's auto-reconcile policy would make that a
cron job.

Modeling it correctly means knowing whether a given state change was the platform's doing
or a human's, which the API does not tell us. `GET .../events` carries the event source
and is the path to doing this properly later. Until then, `state` is an output, and
"start this machine" is `fly machine start` — a runtime action, not desired state.

### Certificates never converge, so they do not block

`POST /v1/apps/{app}/certificates/acme` creates the certificate *request* and returns
201 with the DNS records the user must publish. `status` then sits at
`pending_ownership` / `pending_validation` until those records exist in DNS —
indefinitely, if the user never adds them. Fly also rate-limits re-validation
(`rate_limited_until` in the response).

Returning `InProgress` here would hang an apply on an action formae cannot take. So
`Create` returns `Success` as soon as the request is registered, and the DNS
requirements are surfaced as read-only outputs (`dnsRequirements`, `validation`,
`status`) for the user to act on. A certificate is "created" when Fly has accepted the
request; whether it is *valid* is DNS state, not infrastructure state.

### Secrets need a machine restart to take effect

`POST /v1/apps/{app}/secrets` stores the secret and bumps a version, but running machines
keep the environment they booted with. The plugin does **not** auto-restart machines on a
secret change: a restart is a service interruption, and doing it as an invisible
side-effect of a secret update is not something a user can predict or opt out of. Formae's
DAG covers the common case correctly — a Machine that references a Secrets resource is
created after it — and for the update case the README and the example say to
`fly machine restart` (or bump the machine's config to force a new version).

---

## Error mapping

`pkg/transport/fly` owns this. HTTP status → `resource.OperationErrorCode`:

| HTTP | Code | Note |
|------|------|------|
| 400, 422 | `InvalidRequest` | 422 is Fly's validation failure, including duplicate app name |
| 401 | `InvalidCredentials` | missing/expired/malformed token |
| 403 | `AccessDenied` | valid token, insufficient scope — a read-only or app-scoped token |
| 404 | `NotFound` | |
| 409 | `AlreadyExists` | |
| 412 | `InvalidRequest` | lease precondition failed |
| 429 | `Throttling` | |
| 5xx | `ServiceInternalError` | |
| other | `InternalFailure` | |

Fly-specific classification beyond the status code:

- **`IsNotFound` also matches HTTP 400 whose message contains
  `could not find app` / `not found` / `does not exist`.** Flaps returns 400, not 404, for
  operations against a destroyed app. Without this, deleting an app out of band leaves
  every child resource permanently stuck in the inventory: sync reads a 400, classifies
  it as `InvalidRequest`, and never prunes. The match is on the message rather than the
  bare status because a real malformed request is also a 400.
- **`404`/`NotFound` on delete is success.** Delete is idempotent everywhere.
- **`/v1/postgres` returns `404 {"error":"Organization not found"}` for a bad org**, which
  is an `InvalidRequest` about the *org*, not a missing cluster. Only relevant once P2
  lands; noted so it is not mis-mapped then.

## Secrets and opacity — what the SDK can and cannot express

`FLY::Apps::Secrets.values` is `writeOnly`, which keeps it out of drift detection. It is
*not* field-level `opaque`, and that is not an oversight: formae's schema extractor
computes the hint as `opaque = isSecretValueType(fieldType)`, overwriting whatever the
author wrote, and `isSecretValueType` walks nullable and union types but not a generic's
type arguments. A `Mapping`-valued field therefore cannot be marked opaque as a whole on
formae 0.89.0 — setting `opaque = true` in the FieldHint is silently discarded (verified
by inspecting the rendered `Schema.Hints`).

Opacity is available per entry instead. The field is typed
`Mapping<String, (String|formae.Value)>`, so a sensitive entry can be written
`formae.value(x).opaque`, which renders as `{"$value": …, "$visibility": "Opaque"}` and is
hashed at rest by the agent; the plugin still receives a plain string. Plain-string
entries are stored as written. The README and the examples use `formae.value(…).opaque`
for anything that is actually a secret, and that is the guidance to follow until the SDK
can express opacity on a map field.
- The error body's message field is `message` on some upstreams and `error` on others
  (§ Transport). The decoder probes `message`, `error`, `msg` in that order.

Nothing is retried inside the plugin. Formae's rate limiter shapes the request rate and
its reconciler re-drives failed operations; a retry loop here would be a second,
invisible backoff fighting the first.

---

## Rate limiting

`RateLimit()` returns `MaxRequestsPerSecondForNamespace: 3`, scope `Namespace`.

Fly's documented limits are **1 req/s per action with a burst to 3 req/s**, scoped per
identifier (machine ID or app ID depending on the request), with `GET` on a machine
allowed 5 req/s (burst 10), and app deletions capped at 100/min. Formae's rate limiter
has one knob and it is per-namespace, not per-action-per-object. So the choice is between
1 (the documented steady-state floor for the strictest action) and something higher that
exploits the per-action split.

This started at 1, on the reasoning that it was the documented steady-state floor for the
strictest action and that chasing the burst allowance would risk 429s for latency formae
did not need. **That was wrong, and CI is what proved it.**

In the first live CI run, `FLY::Apps::Machine` discovery failed with
`resource not discovered: timeout after 2m0s ... (4 discovery trigger attempts)`, and the
agent logged `Discovery already running, consider configuring a longer interval` ten
times in that one run. Discovery walks all 14 resource types, and five of them fan out one
request per app or per cluster (Secrets, Certificate, IPAddress, SecretKey, and the
Postgres children), so one sweep is dozens of serialised requests. At 1 req/s that is most
of a minute before the machine listing is even reached — the same arithmetic tabulated
under Discovery below, arrived at the hard way.

Raised to 3, which is Fly's documented burst. Two things justify it beyond the doc:
discovery is almost entirely `GET`s, which Fly limits far more loosely than writes (5 req/s
with burst 10 for `GET` machine), and the failing run contained **zero** 429 responses — so
1 req/s was not protecting against anything observable. If sustained 3 req/s does start
drawing 429s, the transport classifies them as `Throttling`, which formae treats as
recoverable and retries: a slower apply rather than a failed one.

The real fix remains per-action limiting in the SDK. A single per-namespace number cannot
distinguish "create machine, 1/s per machine" from "list machines, 5/s" — 3 is the best
available compromise, not a correct model of Fly's limits.

---

## Package layout

```
fly.go                          Plugin: config methods + CRUD dispatch. Never touches HTTP.
main.go                         sdk.RunWithManifest — do not modify.

pkg/transport/fly/
  client.go                     Bearer-auth JSON client: Do(ctx, Request, out).
  errors.go                     APIError, IsNotFound, ClassifyStatus, ClassifyError.

pkg/resources/prov/
  provisioner.go                The Provisioner interface every resource implements.
  results.go                    FailCreate/FailUpdate/FailDelete/FailStatus/SuccessDelete.
  nativeid.go                   ParseTwoPart / JoinTwoPart for "{app}/{child}" ids.

pkg/resources/registry/
  registry.go                   resourceType → Factory map + TargetConfig.

pkg/resources/apps/
  app.go  machine.go  volume.go  secrets.go  certificate.go  ipaddress.go
  *_test.go                     //go:build unit

schema/pkl/core/fly.pkl         Config + the six resource classes + Resolvables.
testdata/                       Conformance formae, one triple per tested resource.
```

Registry pattern, copied from `formae-plugin-supabase` (which took it from
`formae-plugin-k8s`, which took it from `formae-plugin-aws`). Each resource file
`init()`-registers itself; `fly.go` looks the factory up by resource type and hands it a
live client plus the parsed target config. `fly.go` therefore stays constant as resources
are added — which matters, because it is also where the SDK contract lives.

Target config is re-parsed on **every** request rather than cached. The agent issues
operations against different targets in the same plugin process (discovery across two
orgs, or a sync following a CRUD test that used a different target), and caching the
first one it sees pins the plugin to a stale `org` — which for this plugin means listing
the wrong organization's apps. The HTTP client *is* cached, keyed on base URL, because it
holds no target state. (Same bug, same fix, same reasoning as the comment in
`formae-plugin-supabase/supabase.go`.)

---

## Discovery

`List()` per resource type:

- **App** — `GET /v1/apps?org_slug={org}`. Requires `org` in target config.
- **Machine** — `GET /v1/orgs/{org}/machines` in one call, rather than N calls of
  `GET /v1/apps/{app}/machines` after listing apps. This exists specifically for
  cross-app enumeration and keeps discovery inside the conformance harness's 2-minute
  window on an org with many apps.
- **Volume** — `GET /v1/orgs/{org}/volumes`, same reasoning.
- **Secrets / Certificate / IPAddress** — no org-wide endpoint. These fan out: list apps,
  then one call per app. On a large org that is slow, and it is the first thing to
  revisit if discovery times out.

`DiscoveryFilters()` returns `nil`. Fly has no equivalent of a tag to opt out on, and
guessing at name patterns would hide resources a user wanted to import.

`LabelConfig()`: `$.name` by default; `$.hostname` for Certificate, `$.ip` for
IPAddress, `$.appName` for Secrets — the fields those resources actually identify
themselves by.

Pagination is uneven across the API, so the plugin's handling is too:

| Endpoint | Paginated? | What `List()` does |
|----------|-----------|--------------------|
| `GET /v1/orgs/{org}/machines` | yes — `cursor` + `limit` | passes formae's `PageToken` as `cursor`, returns `next_cursor` as `NextPageToken` |
| `GET /v1/orgs/{org}/volumes` | yes — `cursor` + `limit` | same |
| `GET /v1/apps/{app}/certificates` | yes — `cursor` + `limit` (default 25, max 500) | follows the cursor to the end inside one `List()` call |
| `GET /v1/apps?org_slug=` | no | returns the whole array, nil `NextPageToken` |
| `GET /v1/apps/{app}/secrets` | no | as above |
| `GET /v1/apps/{app}/ip_assignments` | no | as above |

Certificates are the odd one out: because `List()` already fans out over every app, a
per-app cursor cannot be threaded through formae's single `PageToken`, so that loop
follows the cursor internally. Stopping at page one would report a partial answer, which
discovery reads as "these are all the certificates" — and prunes the rest.

Both org-wide calls also pass `summary=true`: discovery only needs native ids, and the
full machine config per row is a large payload for nothing.

---

## Adding a resource

1. Read the operation out of `https://docs.machines.dev/spec/openapi3.json` — not from
   a Terraform provider, and not from memory. The spec is what the API does.
2. Add the class to `schema/pkl/core/fly.pkl`: `@formae.ResourceHint` with `type` and
   `identifier` (a JSONPath into the `Read` output), `createOnly` on anything the API has
   no update path for, `writeOnly` on anything the API never echoes back. If it hangs off
   an app, add an `appName: String|formae.Resolvable` field and a `Resolvable` subclass so
   other resources can reference its outputs.
3. Write the failing unit test first (`//go:build unit`), against an `httptest.Server`.
4. Implement `prov.Provisioner` in `pkg/resources/apps/<name>.go`, `init()`-register it
   with the operations it genuinely supports — a registered operation that returns
   `ErrNotImplemented` is worse than an unregistered one.
5. `testdata/<name>.pkl` + `-update.pkl` (+ `-replace.pkl` if it has `createOnly` fields),
   and extend `scripts/ci/clean-environment.sh` if it can leak outside an app.
6. `make lint && make test-unit && make install && make conformance-test TEST=<name>`.
   Conformance runs the **installed** binary — `make install` is not optional.
