## Description

<!-- What does this change and why? Link the issue if there is one. -->

Fixes #

## Checklist

<!-- Tick what applies; delete lines that do not. See CONTRIBUTING.md for details. -->

- [ ] `make fmt vet lint test` passes locally
- [ ] `*_types.go` changed: ran `make manifests generate sync-chart-crds` and committed the output
- [ ] `values.yaml` changed: updated `charts/exalsius-operator/README.md` values tables and `values.schema.json`
- [ ] Behaviour or API changed: updated README, chart README or examples
- [ ] Architectural decision: added or updated an ADR under `docs/adr/`
- [ ] Workspace path changed: added the `e2e` label to run the multi-cluster suite
- [ ] Breaking change: PR title carries `!` and the description explains the migration

## Notes for reviewers

<!-- Anything that helps review: what to look at first, what you are unsure about, how you tested it. -->

<!--
PR titles must follow Conventional Commits; the title becomes the squash-commit message
and release-please builds the CHANGELOG from it:

  feat(scope): ...      new capability            -> minor version bump
  fix(scope): ...       bug fix                   -> patch version bump
  feat(scope)!: ...     breaking change           -> minor bump while pre-1.0, called out in CHANGELOG
  docs | chore | refactor | test | ci             -> no release

Common scopes: colony, workspace, chart, ci, docs.
Do not edit CHANGELOG.md or the chart version by hand.

Squash-merging commits from several authors? Add Co-authored-by trailers so credit is kept:

  Co-authored-by: Name <email@example.com>

Reference: https://www.conventionalcommits.org/en/v1.0.0/
-->
