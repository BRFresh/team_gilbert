# Deploy the scanner from feat

## What runs where

GitHub Actions builds the container image and pushes it to GHCR. Argo CD reads
Kubernetes manifests from Git and applies them to OpenShift. OpenShift pulls the
image and runs the pod. Use Argo CD to manage this deployment; it is not a
container runtime or an image builder.

Use one Argo CD Application for the scanner:

| Application     | Git revision | Source path            | Destination namespace |
| --------------- | ------------ | ---------------------- | --------------------- |
| upgrade-checker | feat         | deploy/upgrade-checker | upgrade-checker       |

Paths are relative to the repository root: do not add a leading slash.
The scanner manifests are split by function in `deploy/upgrade-checker`:

| File | Resources |
| --- | --- |
| `namespace.yaml` | The `upgrade-checker` namespace. |
| `app.yaml` | The Go scanner Deployment, image, arguments, and health probes. |
| `serviceaccount.yaml` | The scanner's ServiceAccount. |
| `rbac.yaml` | The ClusterRole and ClusterRoleBinding for API reads. |
| `service.yaml` | The Service forwarding port 80 to the app's port 8080. |
| `route.yaml` | External HTTP access through the scanner Service. |
| `servicemonitor.yaml` | Prometheus discovery and scrape settings. |

All namespaced scanner resources use `upgrade-checker`.
Argo CD reads all of these files from the same source folder.
The Argo CD Application definition itself is stored separately
in `gitops/upgrade-checker-application.yaml`, in the GitOps controller's
`openshift-gitops` namespace.

The scanner exports live `upgrade_checker_*` metrics. The `manifests/` folder
contains only the cluster-wide monitoring configuration, which belongs in
`openshift-monitoring`; it is not an application source path.

## First deployment

1. Merge/push these changes into **feat**. Check your current local branch first
   with `git branch --show-current`; changes on another branch do not reach feat
   automatically.
2. In GitHub Actions, wait for **Publish container image** to succeed. The workflow
   now accepts pushes to main and feat when app/build/workflow files change.
   It tests the app and publishes both `sha-<commit>` and the branch tag, including
   `ghcr.io/brfresh/team_gilbert:feat`. It uses GitHub's temporary `GITHUB_TOKEN`.
3. Set the GHCR package visibility to **Public** in its package settings. A public
   Git repository does not automatically make its container package public.
4. After the image exists, update the existing Argo CD Application:

```sh
oc apply -f gitops/upgrade-checker-application.yaml
```

Alternatively, edit the existing application's source in Argo CD:
**Target revision = feat**, **Path = deploy/upgrade-checker**.
Keep destination namespace **upgrade-checker**.

Automatic sync is enabled. If an existing Application still watches `manifests/`,
update its source to the scanner path above and ensure only one Application owns
the scanner resources. Review pruning and resource ownership when migrating an
existing deployment.

Argo CD must be allowed to manage the included ClusterRole and ClusterRoleBinding.
If sync reports forbidden access, a cluster administrator must grant the GitOps
controller appropriate permissions or manage those two RBAC objects separately.

## Credentials

Do not put your laptop kubeconfig, an `oc login` token, or registry credentials
in the image. The final image contains only the compiled program and public CA
certificates. Its build context includes only the required Go files.

OpenShift starts the pod as ServiceAccount `upgrade-checker`, mounts its rotating
token and cluster CA, and supplies the Kubernetes API address. `-in-cluster`
selects `rest.InClusterConfig()`, which uses these credentials. The ClusterRole
permits only listing nodes, PDBs across namespaces, ClusterOperators, and
APIRequestCounts, plus `get` on `clusterversions/version`. Anyone downloading the public image gets code, not this identity.

## Verify

```sh
oc -n upgrade-checker rollout status deployment/upgrade-checker
oc -n upgrade-checker logs deployment/upgrade-checker
oc -n upgrade-checker port-forward service/upgrade-checker 8080:80
```

In another terminal:

```sh
curl http://localhost:8080/metrics
```

All five `upgrade_checker_collection_success` samples should equal 1. API failures
are logged and appear as 0. Both health probes check the HTTP process, not API
permissions.

For external access, `deploy/upgrade-checker/route.yaml` targets the same Service
on its named `http` port. Argo CD includes this file automatically from the source
folder. OpenShift assigns the route hostname because `spec.host` is omitted.
After syncing, retrieve it with:

```sh
oc -n upgrade-checker get route upgrade-checker -o jsonpath='{.spec.host}'
```

Open `http://<route-host>/metrics` for live metrics, or
`http://<route-host>/healthz` for process liveness. The Route serves plain HTTP
without authentication, like the app. Prometheus continues using the internal
Service through the ServiceMonitor.

`deploy/upgrade-checker/servicemonitor.yaml` selects the scanner Service by
`app: upgrade-checker`, scrapes its named `http` port at `/metrics` every 30 seconds,
and allows 10 seconds per scrape (longer than the app's 5-second API timeout).
The Service forwards to the container's named `metrics` port on 8080. Both health
probes use `/healthz` on that same container port.

The ServiceMonitor requires the Prometheus Operator CRD and enabled OpenShift
user workload monitoring. A cluster administrator can merge
`enableUserWorkload: true` into the existing `cluster-monitoring-config` in `openshift-monitoring`;
`manifests/cluster-monitoring-config.yaml` shows that setting. Keep other existing
monitoring configuration when enabling it. See the
[OpenShift user workload monitoring guide](https://docs.redhat.com/en/documentation/monitoring_stack_for_red_hat_openshift/4.20/html-single/configuring_user_workload_monitoring/configuring_user_workload_monitoring).

The collector URL is:

```text
http://upgrade-checker.upgrade-checker.svc:80/metrics
```

NetworkPolicy must permit collector-to-Service and app-to-API traffic. The image
supports Linux amd64/arm64, arbitrary non-root UIDs, and a read-only filesystem.
Resource requests/limits are initial values; adjust them based on cluster usage.

## Subsequent releases

The `feat` tag is convenient for the first deployment. Publishing a new image
under the same tag does **not** restart an existing pod. `imagePullPolicy: Always`
only pulls when a container starts.

For repeatable GitOps releases, copy the digest reference from the successful
workflow summary into `deploy/upgrade-checker/app.yaml`:

```yaml
image: ghcr.io/brfresh/team_gilbert@sha256:<published-digest>
```

Commit that change to feat. The changed Deployment then triggers a rollout.
Manifest-only changes intentionally do not rebuild the image.

## Perses dashboard

The same Argo CD source directory includes a COO Perses dashboard and a
namespace-scoped Thanos datasource. Dashboard-only commits on `feat` sync through
Argo CD without an image rebuild. See [Perses setup](perses.md) for the exporter
version prerequisite, verification commands, scrape labels, and finding semantics.
The ServiceMonitor also requires user workload monitoring to be enabled. If the
instance selector is empty, follow the [monitoring prerequisite checks](perses.md#monitoring-prerequisite-and-missing-instances).

## References

- [Argo CD application source and destination](https://argo-cd.readthedocs.io/en/latest/user-guide/application-specification/)
- [Kubernetes ServiceAccounts](https://kubernetes.io/docs/concepts/security/service-accounts/)
- [OpenShift Routes](https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html/ingress_and_load_balancing/routes)
- [GitHub Container Registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
