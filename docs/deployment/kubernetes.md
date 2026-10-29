# Running divisor on Kubernetes

Divisor runs on Kubernetes as a plain Deployment that reads a config file
from a ConfigMap and resolves its Backends through DNS; it never calls the
Kubernetes API. Install it with the Helm chart or with the example manifests;
both deploy the same thing with the same divisor config.

## How divisor fits in

A Kubernetes Service balances across pods itself, so pointing divisor at one
gives it a single Backend. To let divisor choose among pods (least-connection,
ip-hash, least-response-time, ...), give each pod a stable DNS name and list
each one as a Backend. The usual way is a StatefulSet behind a headless
Service: StatefulSet `app` with headless Service `app` (`clusterIP: None`)
gives its pods the names `app-0.app`, `app-1.app`, `app-2.app`, which is what
the default config lists:

```yaml
backends:
  - url: app-0.app:8080
  - url: app-1.app:8080
  - url: app-2.app:8080
```

The short names resolve within divisor's own namespace; for Backends in another
namespace use `app-0.app.<namespace>.svc.cluster.local:8080`.

Divisor can start before its Backends: a Backend that does not answer yet is
Down, divisor answers `503` while nothing is Alive, and each Backend Rejoins as
soon as a Probe succeeds. Rollout order does not matter.

## Install with Helm

Every release publishes a chart to GHCR, versioned with the release and
defaulting to that release's image:

```bash
helm install divisor oci://ghcr.io/aaydin-tr/charts/divisor --version 1.0.0 -f values.yaml
```

The chart deploys a Deployment, a ConfigMap holding the divisor config, and a
Service on port `80` in front of divisor's `port`. Its values
([`charts/divisor/values.yaml`](../../charts/divisor/values.yaml)):

| Value | Default | Meaning |
| --- | --- | --- |
| `replicaCount` | `2` | divisor pods |
| `image.repository` | `ghcr.io/aaydin-tr/divisor` | image to run |
| `image.tag` | the chart's own release, e.g. `v1.0.0` | image tag |
| `terminationGracePeriodSeconds` | `40` | see [Rolling updates and shutdown](#rolling-updates-and-shutdown) |
| `config` | the config shown in [`examples/kubernetes/configmap.yaml`](../../examples/kubernetes/configmap.yaml) | the divisor config, written out as `config.yaml` |

A minimal `values.yaml` only replaces the Backend list:

```yaml
config:
  backends:
    - url: api-0.api:8080
    - url: api-1.api:8080
```

Helm merges your `config` into the default one key by key, so
`server.dns_cache_duration`, `monitoring.host` and the ports stay as they are
unless you set them. Lists are not merged: your `backends` replaces the default
list whole. The chart reads `config.port` and `config.monitoring.port` for the
container ports, and its probes need `config.monitoring.host: 0.0.0.0`, so keep
that one. Every config key is in [Configuration](../configuration.md).

Resources are named after the release: a release whose name contains `divisor`
gets that name (`divisor`, `divisor-config` for the command above), any other
release `<release>-divisor` (`<release>-divisor-config`). The `kubectl`
commands on this page use `deployment/divisor` and `divisor-config`, the names
from the command above and from the example manifests; substitute yours if you
named the release differently. The chart has
no Ingress, HorizontalPodAutoscaler or PodDisruptionBudget; add your own around
it if you need them.

`helm upgrade` with new `config` values updates the ConfigMap only, so the pods
keep running and pick it up as a Reload (see
[Reload by editing the ConfigMap](#reload-by-editing-the-configmap)). A change
outside the reloadable keys needs a restart afterwards:

```bash
kubectl rollout restart deployment/divisor
```

## Install with plain manifests

[`examples/kubernetes/`](../../examples/kubernetes/) holds the same deployment
as three manifests:

- [`configmap.yaml`](../../examples/kubernetes/configmap.yaml): ConfigMap
  `divisor-config` with the divisor config under the key `config.yaml`. It
  lists the three StatefulSet pods as Backends, binds the Monitoring server on
  `0.0.0.0` for the kubelet's probes, and sets `server.dns_cache_duration: 5s`.
- [`deployment.yaml`](../../examples/kubernetes/deployment.yaml): two divisor
  replicas running as the image's nonroot user with a read-only root
  filesystem and no service account token, the ConfigMap mounted as the
  directory `/etc/divisor`, the probes, and the shutdown settings below.
- [`service.yaml`](../../examples/kubernetes/service.yaml): Service `divisor`,
  port `80` to the container's `http` port (`8080`).

The Deployment runs `ghcr.io/aaydin-tr/divisor:latest`; pin a release tag
(`v1.0.0`) in production. Edit the Backend list in `configmap.yaml`, then:

```bash
kubectl apply -f examples/kubernetes/
```

Clients inside the cluster reach divisor at `http://divisor` (or
`divisor.<namespace>.svc.cluster.local`); expose it outside the cluster with an
Ingress or a `LoadBalancer` Service of your own.

## Probes

The Monitoring server answers the kubelet's probes on its port (named
`monitoring`, `8001`):

- **liveness**: `/healthz`, `200` whenever the process runs.
- **readiness**: `/ready`, `200` once the listeners are bound, `503` from the
  moment graceful shutdown begins.

Neither depends on Backend liveness. With every Backend Down, divisor stays
live and ready and answers `503`: the kubelet never restarts it for an outage
it did not cause, and the Service keeps sending traffic to it, so the `503`s
stay visible.

The Monitoring server binds `localhost` by default and the kubelet probes the
pod IP, so the config must set `monitoring.host: 0.0.0.0`; both the chart and
the example do.

## Rolling updates and shutdown

On `SIGTERM` divisor flips `/ready` to `503`, then at once stops accepting
connections and drains in-flight
requests for up to `server.graceful_shutdown_timeout` (default `30s`). The
Service only stops routing to a terminating pod a moment later, so the
Deployment delays `SIGTERM` with a 5-second `preStop` sleep, and
`terminationGracePeriodSeconds: 40` leaves room for the sleep plus the full
drain before the kubelet sends `SIGKILL`. If you raise
`graceful_shutdown_timeout`, raise the grace period with it (`terminationGracePeriodSeconds`
in the chart's values).

## Reload by editing the ConfigMap

Divisor applies an edited config without a restart when only `backends`,
`health_checker_time` or `health_checker_timeout` change. Edit the ConfigMap
(`kubectl edit configmap divisor-config`, `kubectl apply`, or `helm upgrade`),
and:

1. The kubelet updates the mounted file, which can take about a minute
   (its sync period plus its ConfigMap cache).
2. Each divisor pod notices the change and Reloads about a second later:
   added Backends are Probed and join if Alive, removed ones finish their
   in-flight requests and leave, and unchanged ones keep their liveness and
   stats.

An edit that touches any other key, or that does not validate, is rejected as a
whole: divisor keeps serving the running config and logs which keys need a
restart. Apply those with `kubectl rollout restart deployment/divisor`. The
rules are in [Configuration](../configuration.md#reload).

Check the outcome in each pod's logs:

```bash
kubectl logs -l app.kubernetes.io/name=divisor --prefix --tail=-1 | grep Reload
```

Each Reload logs `Reload applied` with the added and removed Backends, or
`Reload rejected, divisor keeps serving the running config` with the reason.

Mount the ConfigMap as a directory, as both the chart and the example do. A
`subPath` mount is never updated by the kubelet, so no Reload would ever
happen. The image has no shell, so `SIGHUP` cannot be sent from inside the pod;
on Kubernetes the ConfigMap edit is the Reload trigger.

## DNS cache

Divisor reuses a resolved Backend IP for `server.dns_cache_duration` (default
`60s`), both for traffic and for Probes. Pod IPs change whenever a pod is
replaced: with the default, divisor could keep dialing the old pod's IP for up
to a minute after `app-0` comes back with a new one. Both the chart and the
example set `5s`, so a replaced pod is reached within seconds. See
[Configuration](../configuration.md#server-settings).

## Monitoring and logs

Reach a pod's Monitoring server with a port-forward:

```bash
kubectl port-forward deployment/divisor 8001:8001
# http://localhost:8001/ for the dashboard, /stats, /metrics
```

Each pod serves its own `/metrics`; point Prometheus at the container port
named `monitoring`.

Application logs go to stderr and the Access log (`logging.access_log: true`)
to stdout. `kubectl logs` shows both together, but the container runtime
records each line's stream in the node's log files, so a node log agent
(Promtail, Grafana Alloy, Fluent Bit) can route the Access log and Application
logs apart. See [Observability](../observability.md).
