# OpenShift upgrade readiness dashboard

The first dashboard uses the existing Cluster Observability Operator (COO)
**Observe → Dashboards (Perses)** integration and the existing ServiceMonitor.
It does not install another metrics collector or Perses server.

## GitOps resources

- `deploy/upgrade-checker/perses-datasource.yaml` creates the project-scoped
  `upgrade-checker-thanos` datasource in `upgrade-checker`.
- `deploy/upgrade-checker/perses-dashboard.yaml` creates the `upgrade-readiness`
  dashboard in the same namespace. Queries explicitly select that datasource.

Both use `perses.dev/v1alpha2`, supported by the installed COO. Dashboard content
is nested under `spec.config` in this API version. The datasource queries
OpenShift's Thanos Querier on its in-cluster HTTPS endpoint, port 9091, which
includes the existing user workload metrics. COO creates the Perses secret
`upgrade-checker-thanos-secret` from the datasource's TLS configuration. The CA
path `/ca/service-ca.crt` matches the COO setup. No login token is committed.
This follows the [official OpenShift datasource example](https://github.com/perses/perses-operator/blob/main/config/samples/openshift/thanos-querier-datasource.yaml).

The namespace-scoped datasource is explicitly named and is the project's default
Prometheus datasource. COO also needs that default for frontend lookups without
an explicit datasource name. Users need permission to view the
dashboard and datasource in `upgrade-checker`, and permission to query the
underlying monitoring data. See [COO dashboard access and RBAC](https://docs.redhat.com/en/documentation/red_hat_openshift_cluster_observability_operator/1-latest/html/ui_plugins_for_red_hat_openshift_cluster_observability_operator/perses-dashboard).

The Argo CD Application already watches `feat` at `deploy/upgrade-checker`.
Commit these YAML files to that branch and let Argo CD sync them. No image build
is needed for a dashboard-only change, so the Actions workflow's path filters
do not need to change. The scanner image still needs to match the Go exporter
version used by the dashboard.

```sh
git branch --show-current
oc apply --dry-run=server -f deploy/upgrade-checker/perses-datasource.yaml \
  -f deploy/upgrade-checker/perses-dashboard.yaml
```

After the Argo CD sync:

```sh
oc -n upgrade-checker get persesdatasource upgrade-checker-thanos -o yaml
oc -n upgrade-checker get persesdashboard upgrade-readiness -o yaml
```

Open **Observe → Dashboards (Perses)**, choose project **upgrade-checker**, and
open **OpenShift upgrade readiness**. Inspect resource status conditions if
the dashboard or datasource has not reconciled. Argo CD must be allowed to manage
both Perses resource kinds in this namespace.

## Monitoring prerequisite and missing instances

The existing ServiceMonitor requires **OpenShift user workload monitoring** to
be enabled. A ServiceMonitor by itself does not run Prometheus. The exporter
Route and `/healthz` can be healthy while no metrics are being collected.

Check the platform configuration and monitoring pods:

```sh
oc -n openshift-monitoring get configmap cluster-monitoring-config -o yaml
oc -n openshift-user-workload-monitoring get pods
```

`cluster-monitoring-config` must contain `enableUserWorkload: true` inside
`data/config.yaml`. If the ConfigMap is absent, restore it with:

```sh
oc apply -f gitops/user-workload-monitoring-config.yaml
```

If it already contains other settings, merge the flag into that configuration
instead of replacing it. Keep this platform ConfigMap under one GitOps owner.
It lives outside the scanner's application directory because it configures the
shared monitoring stack. See [OpenShift's user workload monitoring setup](https://docs.redhat.com/en/documentation/monitoring_stack_for_red_hat_openshift/4.22/html/configuring_user_workload_monitoring/preparing-to-configure-the-monitoring-stack-uwm).

Wait for the user workload Prometheus pods to become Ready, then allow the
ServiceMonitor's 30-second scrape interval and check the queries below. An empty
instance selector means there is no matching current `up` series, or its query
failed. The instance values are pod IPs and ports, so a rollout changes them;
select the current instance after refreshing the dashboard.

The datasource must keep pointing to Thanos's Prometheus query API. The
exporter's `/metrics` URL is a scrape endpoint, not a Perses datasource API.
`/healthz` checks process liveness only; use `collection_success` and `up` to
verify the full data path.

### Argo CD ownership

The live cluster reported `SharedResourceWarning` because `upgrade-checker`
(branch `feat`) and `monitor-test` (branch `gitops-infra`) both manage the scanner
Deployment, Service, ServiceAccount and reader RBAC. Keep those resources under
one Application. A separate monitoring Application should own only its monitoring
resources. Syncing two different branch versions over the same resources makes
rollouts and configuration changes unreliable.

## Exporter version prerequisite

The dashboard requires the `upgrade_checker_*` metrics described in the README.
If the live target exposes only the older `upgrade_ready` and
`upgrade_deprecated_apis` metrics, it is running an older exporter. An existing
ServiceMonitor and a healthy `up` metric do not prove the current scanner version
is running.

Publish the current Go exporter using the existing Actions workflow, then commit
its image digest to the Deployment in `deploy/upgrade-checker/app.yaml` on
`feat`, as described in [Publish and deploy](deploy.md#subsequent-releases).
Argo CD will roll out the changed Deployment. Re-publishing the mutable `feat`
tag alone does not restart an existing pod. Also sync the scanner RBAC from
`app.yaml`, including the ClusterVersion permission.

In **Observe → Metrics**, check:

```promql
up{namespace="upgrade-checker",service="upgrade-checker"}
upgrade_checker_collection_success{job="upgrade-checker"}
```

The first query should equal 1. The second must contain all five resources
(`nodes`, `poddisruptionbudgets`, `clusteroperators`, `apirequestcounts`,
`clusterversion`) with value 1. The dashboard's instance selector is populated
from the scrape target's `up` series, so it remains usable when collection fails
or the older image is still running.

## Reading the dashboard

The page refreshes every 30 seconds. Panel plugins choose their query mode;
summary cards display the last value at the selected time range's end. The
installed Prometheus plugin rejects an explicit `instant` field in query YAML.
Keep the range ending at **now** for
an assessment of current conditions; choosing a historical end shows historical
evidence. Select one exporter instance to avoid combining different scrapes.

- **Collections:** require 5 / 5. A failed read excludes that resource's
  findings, even if partial samples were emitted. A down/missing scrape target
  also excludes findings. Summary cards use `UNKNOWN` rather than zero when
  evidence is unavailable. For a successful empty collection, counts are zero.
- **OpenShift upgrade gate:** reports CVO Upgradeable as CVO ALLOWS, CVO BLOCKED,
  or UNKNOWN. It is not an overall GO/NO-GO result. The conditions table shows
  reasons; inspect the API object for full messages. Minor/major and patch
  upgrade gates can differ.
- **Operators:** shows unavailable/non-upgradeable, degraded/progressing, and
  unknown standard conditions. The summary counts conditions, not distinct
  Operators. Missing conditions remain visible even without a reported reason.
- **Nodes:** counts nodes not Ready. The current node is Ready and uncordoned,
  so the empty node-condition and cordon detail tables are omitted.
- **PDBs:** separates current-status drain blockers from stale controller status.
  The current PDBs all have current status and allow disruptions, so both empty
  detail tables are omitted. Zero allowed disruptions is a drain concern, not
  automatic proof of an incorrect PDB.
- **API removals:** counts observed usage of APIs with a reported removal release.
  The current cluster returns no APIRequestCount objects, so the empty API usage
  detail table is omitted. Zero observed usage is not evidence that workload
  manifests are compatible with the intended upgrade. Request counts are gauges
  and must not be passed to `rate()`.
- **Unmanaged release resources:** exposes explicit CVO unmanaged overrides.
  These may be deliberate in CRC/dev clusters. The exporter does not perform a
  general unsupported-resource or third-party Operator support scan.

The five empty node, cordon, PDB and API usage detail panels were removed after
checking their queries through Perses. The node readiness, PDB and API usage
summary cards remain; use the remediation commands when a count becomes positive.
The final panel explains investigation and remediation commands. Empty findings
tables indicate no matches only when the corresponding collection succeeded.
Even with no findings, target-version manifest compatibility and vendor support
need separate verification. This iteration provides a dashboard, not alert rules.

## Scrape labels

Queries match the existing ServiceMonitor's `job="upgrade-checker"` and one
`instance`. The instance selector additionally matches the target namespace and
Service. If those scrape labels change, update the dashboard selectors.

The existing ServiceMonitor uses the default `honorLabels: false`. Its target
label `namespace="upgrade-checker"` collides with the exporter's workload
`namespace` label. Prometheus preserves the workload namespace as
`exported_namespace`. PDB and unmanaged-resource tables and joins deliberately
use that label. If you change the monitor to `honorLabels: true`, adjust these
queries and the target namespace selector together. The monitor is managed by
the existing separate GitOps application and is not duplicated here.
