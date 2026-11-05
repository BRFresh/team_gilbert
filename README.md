# team_gilbert

## KPN Hackathon 2026


### Yusuf, NieveeJan, Cas

## Run locally

Log in with `oc login`, then run:

```sh
cd app
go run .
```

The app reads the same local kubeconfig as `oc`: files listed in `KUBECONFIG`
when set, otherwise `$HOME/.kube/config`. It uses that configuration's current
context and credentials, and does not require running inside the cluster.

To check the configuration location and selected context:

```sh
printf '%s\n' "${KUBECONFIG:-$HOME/.kube/config}"
oc config current-context
```

For a custom config, run `KUBECONFIG=/path/to/config go run .` from `app`.
Run Go from the same user and shell environment as `oc`. If `oc` uses an alias
with `--kubeconfig`, set `KUBECONFIG` to that path for the app as well.
The logged-in user needs permission to list cluster nodes (`oc auth can-i list nodes`).
