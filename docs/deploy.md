# Publish and deploy

## How authentication works

The image is public application code, not a logged-in container. It contains
neither a kubeconfig nor credentials, and does not need the `oc` binary.

1. GitHub Actions builds and publishes using its temporary `GITHUB_TOKEN`
   with `packages: write`. No personal access token is needed for this workflow.
2. OpenShift pulls the public GHCR image anonymously.
3. OpenShift starts the pod with the `upgrade-checker` ServiceAccount in the
   `upgrade-checker` namespace and mounts a rotating token and cluster CA.
4. The app's `-in-cluster` flag selects `rest.InClusterConfig()`. Client-go reads
   the mounted credentials and the API address provided by OpenShift. It rereads
   the token file as the token rotates.
5. The ClusterRoleBinding grants only `list` access to the four required resource
   types. Cluster-wide access is required for nodes, operators, API request counts,
   and PDBs across namespaces. It grants no write access or access to Secrets.

Anyone downloading the image receives only the program. Its ServiceAccount
identity and permissions exist in your cluster and are injected at runtime.

Sources: [Kubernetes ServiceAccounts](https://kubernetes.io/docs/concepts/security/service-accounts/)
and [GitHub Container Registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

## 1. Publish the image first

Commit the Dockerfile, `.dockerignore`, and `.github/workflows/publish-image.yaml`
together with the app source to main. Leave `manifests/app.yaml` unchanged at
this stage so Argo CD continues running the existing app until the image exists.

The **Publish container image** Actions workflow tests the app and publishes:

```text
ghcr.io/brfresh/team_gilbert:sha-<full-git-commit>
```

It builds Linux amd64 and arm64 images. The workflow summary prints the exact
`ghcr.io/brfresh/team_gilbert@sha256:...` reference to deploy.
You can also run the workflow manually from GitHub's Actions tab.

After the first successful publication, open the package's **Package settings**
and change its visibility to **Public**. A new GHCR package defaults to private,
even if the repository is public. Verify that an unauthenticated pull succeeds
before deploying; a public package needs no `imagePullSecrets`.

If an existing package with this name rejects publication, grant this repository
Actions access in the package settings. The workflow uses the repository token,
not your local `gh` or Docker credentials.

## 2. Replace manifests/app.yaml

The prepared replacement is `docs/app-deployment.yaml`. It is outside the
Argo CD watched `manifests` directory until you are ready.

1. Replace `REPLACE_WITH_IMAGE_DIGEST` with the 64-character digest from the
   workflow summary (the template already includes `sha256:`).
2. Copy the completed file to `manifests/app.yaml`.
3. Commit and push that manifest change to main, then sync Argo CD if automatic
   sync is not enabled.

The Deployment and Service keep the existing names and selector, allowing the
Deployment to be updated without changing its immutable selector. The new
ServiceAccount and cluster RBAC objects are included in the same file. The
namespace `upgrade-checker` must already exist, or be created by Argo CD.

Argo CD's application project and controller must be allowed to manage the
ClusterRole and ClusterRoleBinding. If they are not, ask a cluster administrator
to manage these two objects separately; the app itself does not need admin rights.

The example omits the old public Route because the collector runs inside
OpenShift. With Argo CD pruning enabled, the old Route will be deleted when the
replacement syncs. Otherwise remove the old `test-web-route` explicitly if you
no longer want it: removing YAML alone does not remove a live object without
pruning. Keeping that Route would expose the unauthenticated metrics externally.

The existing Service remains on port 80 and forwards to the app on port 8080.
The in-cluster collector URL is:

```text
http://test-web-service.upgrade-checker.svc:80/metrics
```

NetworkPolicy must allow the collector to reach the Service and the app to reach
the OpenShift API. Resource limits in the example are starting values; check
actual memory and CPU usage on your cluster. Both probes check HTTP liveness;
API failures are represented by the collection-success metrics, not pod restarts.

## 3. Verify

```sh
oc -n upgrade-checker rollout status deployment/test-web-app
oc -n upgrade-checker logs deployment/test-web-app
oc -n upgrade-checker port-forward service/test-web-service 8080:80
```

In another terminal:

```sh
curl http://localhost:8080/metrics
```

All four `upgrade_checker_collection_success` samples should equal 1. A 0 with
`Forbidden` in the logs indicates an RBAC issue; image pull errors occur before
the app starts and indicate registry visibility, image reference, or connectivity.

Each future release follows the same sequence: publish image, then commit the
new digest into the manifest. Pushing a new registry tag alone does not change
the Deployment or trigger an Argo CD rollout.
