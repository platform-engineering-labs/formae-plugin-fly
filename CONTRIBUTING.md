# Contributing

This document covers local development for plugin authors. For user-facing
plugin docs (configuration, supported resources, examples), see
[README.md](README.md).

## Prerequisites

- Go 1.25+
- [Pkl CLI](https://pkl-lang.org/main/current/pkl-cli/index.html)
- Cloud provider credentials (for conformance testing)

## Local Installation

The Hub-facing install path for end users is `formae plugin install
<publisher>/<plugin>` — that pulls signed artifacts from the orbital
repo. For plugin authors building locally, install from source:

```bash
make install
```

This builds the plugin binary and installs it into your local formae
plugin directory so the agent picks it up on the next start.

## Building

```bash
make build           # Build plugin binary (also writes schema/pkl/VERSION)
make test-unit       # Unit tests (//go:build unit) — no credentials needed
make lint            # golangci-lint
make verify-schema   # Validate the Pkl schema
make install         # Build + install to ~/.pel/formae/plugins/fly/v<version>/
```

`make build` and `make verify-schema` both depend on the `schema-version`
target, which writes `schema/pkl/VERSION`. That file is gitignored — the Hub
build pipeline writes the release version there before packaging — and
`schema/pkl/PklProject` reads it, so any target that evaluates the schema has to
create it first or pkl fails with `Cannot find resource VERSION`.

## Local Testing

```bash
export FLY_API_TOKEN=$(fly auth token)   # or FLY_ACCESS_TOKEN
export FLY_ORG=<your org slug>           # the real slug, not "personal"

make install
formae apply --mode reconcile --yes examples/basic/main.pkl
formae command status <id> --output-layout detailed
```

## Conformance Testing

Conformance runs the **installed** binary, so `make install` is not optional —
the `conformance-test` targets depend on it.

Fixtures live in `testdata/`, one test case per `<name>.pkl`, with optional
`<name>-update.pkl` (in-place update of mutable fields) and
`<name>-replace.pkl` (replacement via a `createOnly` field) variants that the
harness pairs automatically. Files under `testdata/config/` are shared helpers,
not test cases. The harness sets `FORMAE_TEST_RUN_ID` so names are unique per
run.

```bash
export FLY_API_TOKEN=... FLY_ORG=...
export FLY_TEST_REGION=fra           # optional, defaults to fra

make conformance-test                        # everything, CRUD + discovery
make conformance-test TEST=app               # one case
make conformance-test TEST=app,secrets       # several (comma-separated, not a regex)
make conformance-test TEST=postgres TIMEOUT=30
make conformance-test VERSION=0.88.1         # pin the agent version
```

`TIMEOUT` is in **minutes**. The Makefile appends the `m` for `go test -timeout`
and also exports the raw number as `FORMAE_TEST_TIMEOUT`, which the harness reads
for its per-command polling deadline — so `TIMEOUT=15m` would be rejected as
`-timeout 15mm`. `VERSION=latest` is normalised to unset, because the harness
parses `FORMAE_VERSION` as a semver.

**What a run costs.** `app`, `secrets`, `ipaddress` and `secretkey` are free.
`volume` provisions 1 GB of storage. `machine` bills per second for a
`shared-cpu-1x`. `postgres` is the expensive one: a real Managed Postgres
cluster with no free tier, and the slowest case at roughly four minutes. A full
pass takes about 18 minutes.

**Two conformance runs cannot share an organization.**
`scripts/ci/clean-environment.sh` deletes every app and Postgres cluster
matching the `formae-sdk-test-` prefix in `FLY_ORG`, before and after each test,
so parallel runs destroy each other's in-flight resources. CI enforces this with
a workflow-level `concurrency` group; locally, just don't run two at once.

Cleanup runs automatically before and after each test, and can be invoked
directly:

```bash
make clean-environment    # needs jq
```

Deleting an app cascades to its machines, volumes, secrets, certificates and IP
assignments, so one call is enough to stop a leaked machine billing. Postgres
clusters are org-scoped and are swept separately.

### Adding a resource

The full workflow is at the end of [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).
The short version: read the operation out of
`https://docs.machines.dev/spec/openapi3.json` rather than from a Terraform
provider or from memory, write the failing unit test first, and register only
the operations the API actually supports — a registered operation that the
provisioner refuses is worse than an unregistered one.

Be warned that the spec is wrong in places. Cases found by testing against the
live API, all documented where they bite: `POST /v1/apps` returns
`{id, created_at}` rather than the declared `{token}`; `org_slug` on an IP
assignment is rejected for every type except `private_v6`; a deleted volume
answers 200 with `state: "waiting_for_detach"` rather than 404; `SecretKey.type`
is required despite being declared optional, and immutable despite the endpoint
being create-or-update.

## Publishing to the Hub

`formae-plugin.pkl` declares `license = "FSL-1.1-ALv2"`, matching every
PEL-maintained plugin (aws, gcp, ovh, supabase, vercel, azure, oci). The
scaffold comment claiming the Hub only accepts `Apache-2.0`, `BSD-3-Clause`,
`MIT` or `MPL-2.0` does not reflect what the shipped plugins do.

`schema/pkl/PklProject` publishes to
`package://hub.platform.engineering/plugins/fly/schema/pkl/fly/fly`. The doubled
path segment is deliberate and matches aws, gcp and ovh: `formae extract`
resolves the schema package from exactly that URL, and a single segment makes
extraction fail with a 404.

For the full publishing flow, see the
[Plugin SDK Documentation](https://docs.formae.io/plugin-sdk).
