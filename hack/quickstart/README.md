# Quickstart helper scripts

Plumbing for [docs/quickstart.md](../../docs/quickstart.md). The scripts install
the parts a reader should not have to type; the Colony and WorkspaceDeployment
in [examples/quickstart/](../../examples/quickstart/) stay visible YAML.

| File | Purpose |
|---|---|
| `setup-management.sh` | One SSH node → k0s management cluster with OpenEBS, k0rdent/KCM, exalsius-operator, the `exalsius-remote-cluster` ClusterTemplate, the SSH Credential for worker nodes, and the demo workspace catalog. |
| `child-kubeconfig.sh` | Prints a child cluster's kubeconfig from its k0rdent Secret. |
| `versions.env` | Every version pin, in one place. |
| `manifests/` | Static manifests the setup script applies. |
| `lib.sh` | Shared shell helpers. |

Requirements on your workstation: `bash`, `ssh`, `kubectl`, `helm`, and
[`k0sctl`](https://github.com/k0sproject/k0sctl/releases) at the version in
`versions.env`. The SSH key must work non-interactively (no passphrase, or
loaded into `ssh-agent`) as root on both nodes.

```bash
hack/quickstart/setup-management.sh --node <mgmt-ip> --ssh-key ~/.ssh/quickstart
export KUBECONFIG=$PWD/quickstart-out/mgmt.kubeconfig
```

The script is safe to re-run. Output (generated `k0sctl.yaml`, kubeconfig)
lands in `./quickstart-out/`, which is git-ignored.

## Bumping versions

All pins live in `versions.env` and are bumped together, then the guide is
re-run verbatim on fresh nodes before the bump merges. There is no automated
freshness check; the quickstart tracks the operator's supported KCM line, so
bump it whenever the operator moves to a new KCM release.
