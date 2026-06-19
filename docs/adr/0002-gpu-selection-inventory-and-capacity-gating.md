# GPU selection, inventory, and capacity gating

Status: accepted (2026-06-16, revised 2026-06-19)

> **Revision (2026-06-19).** The original decision keyed offering identity *strictly* on the
> canonical `exalsius.ai/gpu-model` label and made any node lacking it invisible to both the
> inventory and the gate. In practice GPU nodes provisioned by the NVIDIA GPU Operator / AMD
> (ROCm) GPU Operator carry rich vendor labels but frequently *not* the canonical one, so real
> clusters showed an empty inventory despite having visible GPUs. This revision makes the
> canonical label **preferred, not required**: every GPU node is now discoverable, the model
> name falls back to a vendor "product" label (or stays empty), and each offering publishes the
> exact **Selector** that picks it so the placeability invariant still holds. The revised text
> is inline below; bullets touched by the revision are marked.

Users need to say which GPU a workspace runs on (`gpuType: H100`, optionally a vendor),
have the operator **fail early** when that GPU isn't available on the target cluster, and
**discover** what's pickable per cluster — across both NVIDIA and AMD. The hard problem is a
vocabulary gap: a user types `H100`, but a node carries long, vendor-specific GPU Feature
Discovery (GFD) labels (`nvidia.com/gpu.product=NVIDIA-H100-80GB-HBM3`; AMD's scheme differs
and is messier), and a `nodeSelector` can only exact-match. We resolve this by splitting GPU
data by **change-rate**, normalizing toward a provisioning-owned canonical label *when present*
while still surfacing and selecting on raw vendor labels when it is not — so the operator is a
faithful reader of whatever the cluster advertises, never blocking discovery on a label the
vendor operators don't set.

## The shape

- **Two change-rates, two homes.** *GPU Inventory* — which GPU **Offerings** exist on each
  Child Cluster and their total counts — is slow-changing (moves only on node join/leave) and
  is published on **`Colony.status.gpuInventory`** (keyed by cluster) by the Colony controller
  on a periodic poll (best-effort per cluster, bounded staleness). *Available Capacity* — how
  many are free *right now* — is fast-changing and is **never cached**; it's computed live
  against the target Child Cluster at gate time. The Colony status holds inventory **only**.

- **Inventory is for discovery; the gate is live.** `Colony.status` exists so the exalsius
  API/CLI can show "what GPU vendor/model/memory can I pick on this cluster?" and resolve a
  fuzzy "any H100 / ≥80GB" request **above the operator**, writing a *concrete* request into
  the `WorkspaceDeployment`. The operator's gate does **not** read Colony status — it live-scans
  the target cluster (it already does, for feasibility), deriving existence and capacity in one
  pass. Both paths call **one shared offering-derivation function**, so the vocabulary the API
  advertises and the vocabulary the gate matches are identical by construction. Non-Colony
  ("adopted") clusters therefore still gate correctly — they just don't appear in the API's
  discovery view.

- **Offering = `(vendor, model, profile, resourceName)`**, aggregated per cluster (not per
  node). Memory is **baked into the canonical model name** (`A100-80GB`, `A100-40GB`, `H100`),
  not a separate axis, so a canonical model name fully disambiguates the hardware. **(Revised)**
  The model name is **best-effort, not required**, derived by precedence: the canonical
  `exalsius.ai/gpu-model` label when provisioning set one; else the first present **vendor
  product label** (NVIDIA `nvidia.com/gpu.product`; AMD an ordered candidate list defaulting to
  `amd.com/gpu.product-name`, then `amd.com/gpu.device-id`; both lists configurable); else empty.
  **Every schedulable node advertising a supported GPU extended resource produces an offering** —
  a node is no longer dropped for lacking the canonical label. To keep "passing the gate implies
  placeable," each offering publishes the **`Selector`** that reproduces it: `{exalsius.ai/gpu-model: <model>}`
  when canonical-backed, `{<product-label>: <value>}` when product-backed, or empty when the node
  carried no usable GPU label (the caller then composes a selector from the retained raw labels).
  Raw GFD/AMD/HAMi labels are still retained as **attributes** for display and selection.
  Consequence of best-effort identity: the *same* physical GPU surfaces as **two offerings** when
  some of its nodes carry the canonical label and others only a vendor label (`H100` vs
  `NVIDIA-H100-80GB-HBM3`) — intentional, because each set needs a different `Selector`; merging
  them would yield an offering no single selector could place on. The original rule — never treat
  an unlabelled node as a pickable *canonical* offering — still holds: such a node is pickable
  only via its raw `Selector`, and `gpuType` sugar remains canonical-only.

- **Request = a GPU Selector; the chart places, the operator gates the same selector.**
  A request is a set of node-label requirements, expressed two ways (both exposed in v1):
  `gpuType` sugar (→ `exalsius.ai/gpu-model`) and a **raw label-selector override** (any
  GFD/AMD label, exact-match — usable even on clusters lacking the canonical label). The
  load-bearing invariant: **the operator gate validates the exact selector the chart will place
  on.** Gate and placement use one identical selector, so whatever label it targets, a passing
  request is placeable — no drift. The chart applies it as a **generic `nodeSelector` map**
  handed in via Helm values and hardcodes no label key. `gpuVendor` is normally **inferred**
  from the resolved offering; the operator **injects `resourceName`** (`nvidia.com/gpu` /
  `amd.com/gpu` / a MIG resource) so the chart requests the correct vendor's extended resource
  via injected `{resourceName, count}` rather than a hardcoded `nvidia.com/gpu`.

- **Discrete-unit GPUs now; fractional deferred.** The generic count-based gate (allocatable
  units of `resourceName` minus requested units) naturally covers **whole GPUs and MIG slices**
  alike — MIG is not special-cased (it rides `profile` in the identity + the MIG `resourceName`;
  to be validated before claimed). Only **fractional/memory-based sharing (HAMi vGPU)** —
  capacity as a memory quantity, discovered via annotations not labels — is out of v1 scope. The
  `profile` + `resourceName` + raw-label fields keep adding it non-breaking.

- **Lifecycle: `Waiting` vs `Failed`.** When the requested offering **exists but none are free**,
  the workspace is held (no Helm release created) in a new **`Waiting`** phase, retried until
  capacity frees — transient, self-resolving, no human action. It is deliberately **not** called
  `Queued`: the operator is not a scheduler. When the requested offering is **absent** from the
  cluster, the workspace goes **`Failed`** with a meaningful message — terminal; the user fixes
  the request (or has the GPU provisioned) and recreates. `Failed` no longer self-heals; genuinely
  transient problems stay in non-`Failed` phases and retry.

- **Best-effort FCFS, not a scheduler.** `Waiting` workspaces are served **oldest-first** by
  `creationTimestamp` (with `UID` as the tiebreaker, since the timestamp is second-granularity):
  a `Waiting` WSD lists its siblings waiting for the same `(cluster, offering)` and proceeds only
  if it is the oldest. This gives deterministic ordering and curbs the double-claim → spurious-
  `Failed` stampede, with **no** priority, gang scheduling, preemption, backfill, or cross-org
  fairness — head-of-line blocking is accepted. Real fairness, if ever needed, is an integration
  with a GPU-aware queue (Kueue/Volcano), not in-house scheduler growth.

## Consequences

- **(Revised) Provisioning *should* label GPU nodes, but the operator no longer requires it.**
  Setting `exalsius.ai/gpu-model` / `exalsius.ai/gpu-vendor` (configurable keys, sensible defaults)
  remains the recommended tenant/cluster-onboarding step — it is what unlocks the clean `gpuType`
  sugar and a stable canonical vocabulary across vendors. But an unlabelled cluster is no longer
  invisible: its GPUs are discovered from vendor (GPU Operator / ROCm) labels and are pickable via
  the offering's published `Selector`. The GFD→canonical-name normalization still belongs in
  provisioning when you want it; the operator just no longer *depends* on it to function.

- **Charts you own must change**: apply a generic `nodeSelector` map from values, and request the
  GPU via injected `{resourceName, count}` instead of a hardcoded `nvidia.com/gpu`.

- **The Colony controller gains a child-cluster node-list responsibility** (and the RBAC for it on
  child kubeconfigs); offering-derivation is a shared package used by both the Colony poll and the
  WSD gate.

- **Discovery is Colony-scoped**: clusters not owned by a Colony gate correctly but don't surface in
  the API's "what can I pick?" view.

- **Inventory is bounded-stale**: a freshly-added GPU node becomes pickable within one poll interval.
  Fail-safe — the live gate under-reports a brand-new node rather than ever claiming a model that
  isn't there.

- **No autoscale awareness in v1**: "offering absent" is judged against currently-present nodes, so a
  cluster that *could* autoscale a GPU node it doesn't yet have will `Failed` rather than wait.
