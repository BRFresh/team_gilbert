# Deploy the scanner from feat

## What runs where

GitHub Actions builds the container image and pushes it to GHCR. Argo CD reads
Kubernetes manifests from Git and applies them to OpenShift. OpenShift pulls the
image and runs the pod. Use Argo CD to manage this deployment; it is not a
container runtime or an image builder.

The two Argo CD applications must use different source paths:

| Application | Git revision | Source path | Destination namespace |
| --- | --- | --- | --- |
| test-app | HEAD (existing setting) | manifests | test-app |
| upgrade-checker | feat | deploy/upgrade-checker | upgrade-checker |

Paths are relative to the repository root: do not add a leading slash.
`manifests/app.yaml` remains the nginx test application. The scanner manifest is
`deploy/upgrade-checker/app.yaml`. The Argo CD Application definition itself is
stored separately in `gitops/upgrade-checker-application.yaml`.

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

Automatic sync is enabled. This changes only the `upgrade-checker` Application;
the separate `test-app` Application keeps its existing source path and namespace.
Pruning removes the copied nginx Deployment, Service, and Route from the
upgrade-checker namespace as the new scanner resources are reconciled.

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
permissions. No public Route is needed for in-cluster collection.

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
- [GitHub Container Registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
