> **⚠️ Do not clone this repository directly!**
>
> Use `formae plugin init` to create your plugin. This command scaffolds a new
> plugin from this template with proper naming and configuration.
>
> ```bash
> formae plugin init my-plugin
> ```

---

## Setup Checklist

*Remove this section and the warning above after completing setup.*

After creating your plugin with `formae plugin init`, complete these steps:

- [ ] Update `formae-plugin.pkl` with your plugin metadata (name, namespace, summary, category, description, license)
- [ ] Choose your license. **The formae Hub only accepts plugins licensed
      under one of: `Apache-2.0`, `BSD-3-Clause`, `MIT`, or `MPL-2.0`.**
      Pick one of these if you intend to publish to the Hub. Copy the
      matching file from `licenses/` to `LICENSE`, then set the `license`
      field in `formae-plugin.pkl` to the same SPDX identifier.
- [ ] Define your resource types in `schema/pkl/*.pkl`
- [ ] Implement CRUD operations in `plugin.go`
- [ ] Update test fixtures in `testdata/*.pkl` to use your resources
- [ ] Update this README (replace title, description, resources table, etc.)
- [ ] Update [CONTRIBUTING.md](CONTRIBUTING.md) if your local dev steps
      differ from the template's defaults
- [ ] Set up local credentials for testing
- [ ] Run conformance tests locally: `make conformance-test`
- [ ] Configure CI credentials in `.github/workflows/ci.yml` (optional)
- [ ] Register the plugin on the formae Hub (the Hub installs its
      GitHub App on your repo, and tag pushes from then on dispatch
      builds via the Hub — no per-repo release workflow needed)
- [ ] Remove this checklist section and the warning box above

For detailed guidance, see the [Plugin SDK Documentation](https://docs.formae.io/plugin-sdk).

For local development workflow (building, testing, conformance), see
[CONTRIBUTING.md](CONTRIBUTING.md).

---

# Example Plugin for formae

*TODO: Update title and description for your plugin*

Example Formae plugin template - replace this with a description of what your plugin manages.

## Supported Resources

*TODO: Document your supported resource types*

| Resource Type | Description |
|---------------|-------------|
| `FLY::Service::Resource` | Example resource (replace with your actual resources) |

## Configuration

Configure a target in your Forma file:

```pkl
new formae.Target {
    label = "my-target"
    namespace = "FLY"  // TODO: Update with your namespace
    config = new Mapping {
        ["region"] = "us-east-1"
        // TODO: Add your provider-specific configuration
    }
}
```

## Examples

See the [examples/](examples/) directory for usage examples.

```bash
# Evaluate an example
formae eval examples/basic/main.pkl

# Apply resources
formae apply --mode reconcile --watch examples/basic/main.pkl
```

## Licensing

The formae Hub accepts plugins under one of: **Apache-2.0**, **BSD-3-Clause**,
**MIT**, or **MPL-2.0**. Plugins under any other license can still be built
and used locally, but cannot be published to the Hub.

See the formae plugin policy:
<https://docs.formae.io/plugin-sdk/>
