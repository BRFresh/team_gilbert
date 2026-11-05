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
only implements the exporter; it does not configure Prometheus or the Cluster
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

Local kubeconfig is the default. For a future pod deployment, `-in-cluster`
explicitly selects its service account. The container and deployment preparation is described in
[Publish and deploy](docs/deploy.md). Argo CD deploys the manifests; OpenShift runs the pod.

## Metrics

All metrics use the `upgrade_checker_` prefix and are gauges.

| Metric suffix | Labels | Meaning |
| --- | --- | --- |
| `collection_success` | `resource` | 1 if that resource type was collected successfully, 0 on API or decoding errors. |
| `node_ready` | `node` | 1 when Ready is True; 0 for False, Unknown, or missing Ready. |
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

Each scrape reads all four resource types concurrently with a shared timeout
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

Check the current user's permissions:

```sh
oc auth can-i list nodes
oc auth can-i list poddisruptionbudgets.policy --all-namespaces
oc auth can-i list clusteroperators.config.openshift.io
oc auth can-i list apirequestcounts.apiserver.openshift.io
```

## Tests

```sh
cd app
go test -race ./...
```

Tests use fake Kubernetes/OpenShift resources and a simulated stalled transport.
They cover metric values, missing fields, permission failures and recovery,
deleted resources, and API timeouts without needing a live cluster.

## Container publishing and OpenShift

See [Publish and deploy](docs/deploy.md) for the GHCR workflow, ServiceAccount
credentials, read-only RBAC, and the prepared replacement for `manifests/app.yaml`.
