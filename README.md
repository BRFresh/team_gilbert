# team_gilbert

## KPN Hackathon 2026

### Yusuf, NieveeJan, Cas

## Run locally

Log in with `oc login`, then start the metrics endpoint:

```sh
cd app
go run .
```

In another terminal:

```sh
curl http://localhost:8080/metrics
```

This is a plain-text Prometheus metrics endpoint, not an HTML dashboard. The
existing OpenShift collection/forwarding setup can read this endpoint. This repo
implements the exporter and a ServiceMonitor for user workload monitoring, and
supplies a [Perses upgrade readiness dashboard](docs/perses.md) for the installed
Cluster Observability Operator. It does not install Prometheus or the Cluster
Observability Operator.

The app reads the same local kubeconfig as `oc`: files listed in `KUBECONFIG`
when set, otherwise `$HOME/.kube/config`. It uses the current context and credentials.
To check the configuration location and selected context:

```sh
printf '%s\n' "${KUBECONFIG:-$HOME/.kube/config}"
oc config current-context
```

For a custom config, run `KUBECONFIG=/path/to/config go run .` from `app`.
Run Go from the same user and shell environment as `oc`. If `oc` uses an alias
with `--kubeconfig`, set `KUBECONFIG` to that path for the app as well.

## Options

```sh
go run . -listen-address=:8080 -api-timeout=5s
```

The default address binds all interfaces so a collector can reach it if network
routing permits. Use `-listen-address=127.0.0.1:8080` to restrict it to localhost.
The endpoint serves plain HTTP without authentication.

Local kubeconfig is the default. For a pod deployment, `-in-cluster`
explicitly selects its service account. The container and deployment preparation is described in
[Publish and deploy](docs/deploy.md). Argo CD deploys the manifests; OpenShift runs the pod.

## Metrics

All metrics use the `upgrade_checker_` prefix and are gauges.

| Metric suffix | Labels | Meaning |
| --- | --- | --- |
| `collection_success` | `resource` | 1 if that resource type was collected successfully, 0 on API or decoding errors. |
| `node_ready` | `node` | 1 when Ready is True; 0 for False, Unknown, or missing Ready. |
| `node_condition` | `node`, `condition`, `status` | True/False/Unknown series for Ready, pressure conditions, NetworkUnavailable, and any additional reported conditions. Missing conditions are Unknown. |
| `node_info` | `node`, `kubelet_version`, `architecture` | Value 1 with the reported kubelet version and architecture. |
| `node_unschedulable` | `node` | 1 when the node is cordoned. |
| `pdb_current_healthy` | `namespace`, `pdb` | Current healthy pod count. |
| `pdb_desired_healthy` | `namespace`, `pdb` | Required healthy pod count. |
| `pdb_expected_pods` | `namespace`, `pdb` | Expected pod count. |
| `pdb_status_current` | `namespace`, `pdb` | 1 when observedGeneration matches generation. Only interpret PDB status when this equals 1. |
| `pdb_spec_info` | `namespace`, `pdb`, `min_available`, `max_unavailable`, `unhealthy_pod_eviction_policy` | Value 1 with budget settings. Integers/percentages are preserved as labels; empty means unset. |
| `clusteroperator_version_info` | `operator`, `component`, `version` | Value 1 for each reported component version. These are component versions, not a selected upgrade target. |
| `api_removal_reported` | `name` | 1 if OpenShift reports a removal release. Does not imply recent use or incompatibility with a selected target. |
| `pdb_disruptions_allowed` | `namespace`, `pdb` | Current `status.disruptionsAllowed`. |
| `pdb_blocked` | `namespace`, `pdb` | 1 when allowed disruptions equals zero. |
| `clusteroperator_condition` | `operator`, `condition`, `status` | One series per True/False/Unknown status, with the current status set to 1. Missing Available, Progressing, Degraded, or Upgradeable conditions are Unknown. |
| `apirequestcount_info` | `name`, `removed_in_release` | Value 1 identifies the resource and reported removal release, if any. |
| `api_requests_window` | `name` | `status.requestCount`, the aggregate request count in the API's reporting window. |
| `api_requests_current_hour` | `name` | `status.currentHour.requestCount`. |

API request counts are windowed values, not cumulative counters; do not apply
`rate()` to them. Missing count fields are omitted instead of reported as zero.
The removal release comes from OpenShift; it is not a determination that a
specific upgrade is blocked. Zero PDB disruptions means voluntary disruptions
are currently blocked, not necessarily that the PDB is misconfigured.

See the [OpenShift APIRequestCount reference](https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html/metadata_apis/apirequestcount-apiserver-openshift-io-v1)
for the source fields.

## Failure handling

Each scrape reads all five resource types concurrently with a shared timeout
(default 5 seconds). Configure the scraper timeout above this value. Only one
scrape is allowed at a time; overlapping requests receive HTTP 503.

API failures are logged and set the affected `collection_success` to 0, while
other resource types still return metrics. HTTP 200 means metrics were served;
check `collection_success` to determine whether the API reads succeeded.
A decoding error can leave partial metrics for that resource type, so only use
its results when collection success is 1. Every scrape starts fresh: deleted
resources and data from earlier failed scrapes are not retained. Empty lists
are successful collections with no corresponding resource samples.

`/healthz` reports process liveness only, independently of cluster availability.
SIGINT/SIGTERM shuts down the HTTP server gracefully.

## Required API access

The credentials need cluster-wide `list` permission for:

- `nodes` in the core API group
- `poddisruptionbudgets` in `policy` (all namespaces)
- `clusteroperators` in `config.openshift.io`
- `apirequestcounts` in `apiserver.openshift.io`

It also needs `get` permission for `clusterversions/version` in `config.openshift.io`.

Check the current user's permissions:

```sh
oc auth can-i list nodes
oc auth can-i list poddisruptionbudgets.policy --all-namespaces
oc auth can-i list clusteroperators.config.openshift.io
oc auth can-i list apirequestcounts.apiserver.openshift.io
oc auth can-i get clusterversions.config.openshift.io/version
```

## Tests

```sh
cd app
go test ./...
```

When CGO and a C compiler are available, also use `go test -race ./...`.

Tests use fake Kubernetes/OpenShift resources and a simulated stalled transport.
They cover metric values, missing fields, permission failures and recovery,
deleted resources, and API timeouts without needing a live cluster.

## Container publishing and OpenShift

See [Publish and deploy](docs/deploy.md) for the GHCR workflow, ServiceAccount
credentials, read-only RBAC, and the scanner manifests in `deploy/upgrade-checker`.
Configure the Argo CD Application to watch `feat` at `deploy/upgrade-checker`,
with destination namespace `upgrade-checker`. This is the only application
deployment; `manifests/` contains only the cluster-wide monitoring configuration.

In `deploy/upgrade-checker/`, `app.yaml` defines the Deployment. The Service,
ServiceAccount, RBAC, namespace, Route, and ServiceMonitor each have separate YAML files.
After Argo CD syncs the manifests, use `oc -n upgrade-checker get route upgrade-checker`
to find the external hostname, then open `http://<route-host>/metrics`.

## Using the extra evidence

The first Perses dashboard and its Thanos datasource are in
`deploy/upgrade-checker/perses-dashboard.yaml` and `perses-datasource.yaml`.
Argo CD deploys both from `feat` alongside the exporter. See
[Dashboard setup and interpretation](docs/perses.md) for rollout prerequisites,
collection health, findings, remediation, and assessment limits.

A PDB with zero allowed disruptions is not automatically misconfigured. Compare
its healthy, required, and expected pod counts and budget settings. Ignore its
status for readiness decisions when `upgrade_checker_pdb_status_current` is 0.

APIs with a reported removal release and requests in the reporting window can
be selected with PromQL (for one cluster/target):

```promql
(upgrade_checker_api_requests_window > 0)
  and on(job, instance, name) (upgrade_checker_api_removal_reported == 1)
  and on(job, instance) (upgrade_checker_collection_success{resource="apirequestcounts"} == 1)
```

Join `apirequestcount_info` to show the removal release. When aggregating multiple
clusters, include your cluster identity label in joins as well. Removal versions
must be interpreted against the selected OpenShift/Kubernetes target version.
No matching API is an empty result, not proof that all manifests are compatible.

The exporter supplies observations. Unsupported-resource policies, target-version
compatibility, remediation text, and the overall Go/No-Go decision remain with
the downstream assessment/dashboard. APIRequestCount findings are exposed through
`/metrics`; there is no separate `/deprecated-apis` endpoint.

## ClusterVersion and actionable upgrade findings

The exporter now reads the same object as `oc get clusterversion version -o json`.
Deploy the updated ClusterRole as well as the image: it adds only `get` access
to the named `clusterversions/version` object. Existing list permissions stay unchanged.

All names below have the `upgrade_checker_` prefix:

| Metric | Meaning |
| --- | --- |
| `clusterversion_info` | Desired release, last completed release from history, and channel. Desired can be an update in progress. |
| `clusterversion_condition` | True/False/Unknown series for CVO conditions. |
| `clusterversion_condition_info` | Reported condition, status, and reason code. |
| `clusterversion_upgradeable` | CVO's Upgradeable condition: 1=True, 0=False, -1=missing/Unknown. Absent on failed collection. |
| `clusterversion_available_update_info` | Versions in status.availableUpdates. Check RetrievedUpdates before interpreting this list; conditionalUpdates are not exported. |
| `clusterversion_unmanaged_override` | Each explicitly overridden release resource, with group/kind/namespace/name and 1 for unmanaged. |
| `clusterversion_unmanaged_overrides` | Total unmanaged release resources; 0 when none are configured. |
| `clusteroperator_condition_info` | Operator condition, status, and reason code. |
| `api_removal_in_use` | 1 when a removal release is reported and status.requestCount is positive; labels identify the API and removal release. Missing request counts produce no sample. |

These are observations, not a complete target-specific GO/NO-GO calculation.
`Upgradeable=False` is a reported blocker for minor/major upgrades; it does not
necessarily block a patch update. `Upgradeable=True` does not verify workloads,
PDBs or target-version compatibility. Evaluate other evidence alongside it.

For a dashboard's **OpenShift upgrade gate** panel, use this query and map
0 to BLOCKED, 1 to ALLOWED BY CVO, -1 to UNKNOWN, and no data to UNKNOWN:

```promql
upgrade_checker_clusterversion_upgradeable
  and on(job, instance) (upgrade_checker_collection_success{resource="clusterversion"} == 1)
```

Show `clusterversion_condition_info{condition="Upgradeable"}` for its reason.
Use `oc get clusterversion version -o json` for full messages; long messages are
not metric labels. Unmanaged release resources provide one concrete configuration
finding, not a generic unsupported-resource inventory. Overrides in a CRC/dev
cluster may be deliberate: do not remove them automatically.

For API findings, use `upgrade_checker_api_removal_in_use == 1`, gated by successful
APIRequestCount collection. A positive value identifies observed usage to investigate,
not proof of incompatibility with an unspecified upgrade target. Synthetic tests
exercise positive findings even when the live cluster has none.

Source: [OpenShift ClusterVersion API](https://docs.redhat.com/en/documentation/openshift_container_platform/4.18/html/config_apis/clusterversion-config-openshift-io-v1).
