# debugsymd

[debugsymd](https://github.com/debugsymd/debugsymd) is a symbol-serving daemon that speaks the Microsoft SymStore and debuginfod protocols, backed by S3 with an on-disk regenerable cache.

## Parameters

### Common parameters

| Name                   | Description                                                       | Value          |
| ---------------------- | ---------------------------------------------------------------- | -------------- |
| `nameOverride`         | Override the chart name portion of resource names                | `""`           |
| `fullnameOverride`     | Fully override the generated resource name                       | `""`           |
| `replicaCount`         | Number of replicas (each owns its own RWO cache PVC)             | `1`            |
| `revisionHistoryLimit` | How many old ControllerRevisions to retain                       | `3`            |
| `podManagementPolicy`  | StatefulSet pod management policy                                | `Parallel`     |
| `updateStrategy.type`  | StatefulSet update strategy                                      | `RollingUpdate`|

### Image parameters

| Name                | Description                                                          | Value                          |
| ------------------- | ------------------------------------------------------------------- | ------------------------------ |
| `image.repository`  | debugsymd image repository                                          | `docker.io/bosiakov/debugsymd` |
| `image.tag`         | Image tag (empty defaults to the chart appVersion)                  | `""`                           |
| `image.pullPolicy`  | Image pull policy                                                   | `IfNotPresent`                 |
| `imagePullSecrets`  | Secrets for pulling from a private registry                         | `[]`                           |

### Daemon configuration

| Name                 | Description                                                        | Value                  |
| -------------------- | ----------------------------------------------------------------- | ---------------------- |
| `bind`               | Public symstore + debuginfod listener address (`-bind`)           | `:8080`                |
| `admin`              | Admin (health/readiness/metrics) listener address (`-admin`)      | `:9090`                |
| `logLevel`           | Log level (`-log-level`): debug/info/warn/error                   | `info`                 |
| `debuginfod`         | Serve debuginfod `/buildid/...` routes (`-debuginfod`)            | `true`                 |
| `cache.dir`          | On-disk cache root (`-cache-dir`); matches the cache mountPath     | `/var/cache/debugsymd` |
| `cache.maxUnusedFor` | Evict cache entries untouched for longer than this                 | `336h`                 |

### S3 parameters

| Name             | Description                                                            | Value |
| ---------------- | --------------------------------------------------------------------- | ----- |
| `s3.bucket`      | S3 bucket holding the symbol store (`-s3-bucket`). **Required**        | `""`  |
| `s3.region`      | S3 region (`-s3-region`); also exported as `AWS_REGION`                | `""`  |
| `s3.endpointUrl` | S3-compatible endpoint URL (`-s3-endpoint-url`), e.g. MinIO/Ceph       | `""`  |
| `s3.keyPrefix`   | Optional prefix for derived blob keys (`-s3-key-prefix`)               | `""`  |

### Sentry parameters

| Name                          | Description                                                                | Value   |
| ----------------------------- | ------------------------------------------------------------------------- | ------- |
| `sentry.apiUrl`               | Sentry API base URL (`-sentry-api-url`); when set, org/project/token req'd | `""`    |
| `sentry.org`                  | Sentry organization slug (`-sentry-org`)                                   | `""`    |
| `sentry.project`              | Sentry project slug (`-sentry-project`)                                    | `""`    |
| `sentry.token.create`         | Create a Secret from `sentry.token.value`                                  | `false` |
| `sentry.token.value`          | Token value, used only when `create=true` (prefer `--set` or a manager)    | `""`    |
| `sentry.token.existingSecret` | Name of a pre-existing Secret holding the token (preferred for prod)       | `""`    |
| `sentry.token.key`            | Key within the Secret that holds the raw token                            | `token` |

### AWS parameters

| Name                            | Description                                                          | Value         |
| ------------------------------- | ------------------------------------------------------------------- | ------------- |
| `aws.region`                    | AWS region (`AWS_REGION`); defaults to `s3.region` when empty        | `""`          |
| `aws.credentials.existingSecret`| Secret holding an AWS shared-credentials file (empty = IRSA chain)   | `""`          |
| `aws.credentials.key`           | Key within the Secret holding the credentials file                  | `credentials` |

### Persistence parameters

| Name                       | Description                                                       | Value             |
| -------------------------- | ---------------------------------------------------------------- | ----------------- |
| `persistence.enabled`      | Use a PVC for the cache (`false` uses an emptyDir)               | `true`            |
| `persistence.storageClass` | StorageClass; `""` = cluster default, `-` = disable provisioning | `""`              |
| `persistence.accessModes`  | PVC access modes                                                 | `["ReadWriteOnce"]` |
| `persistence.size`         | Cache PVC size                                                   | `20Gi`            |
| `persistence.annotations`  | Annotations for the cache PVC                                    | `{}`              |

### Exposure parameters

| Name                                | Description                                                       | Value                   |
| ----------------------------------- | ---------------------------------------------------------------- | ----------------------- |
| `service.type`                      | Public Service type: ClusterIP or LoadBalancer                  | `ClusterIP`             |
| `service.port`                      | Public Service port                                             | `8080`                  |
| `service.annotations`               | Public Service annotations                                      | `{}`                    |
| `service.loadBalancerSourceRanges`  | Restrict source ranges (LoadBalancer only)                     | `[]`                    |
| `adminService.enabled`              | Create a ClusterIP Service for the admin listener              | `false`                 |
| `adminService.port`                 | Admin Service port                                             | `9090`                  |
| `ingress.enabled`                   | Enable Ingress for the public listener                        | `false`                 |
| `ingress.className`                 | IngressClass name                                             | `""`                    |
| `ingress.annotations`               | Ingress annotations                                           | `{}`                    |
| `ingress.hosts`                     | Ingress host/path rules                                       | `[{host: debugsymd.example.com, paths: [{path: /, pathType: Prefix}]}]` |
| `ingress.tls`                       | Ingress TLS configuration                                     | `[]`                    |

### ServiceAccount and RBAC parameters

| Name                           | Description                                                                  | Value   |
| ------------------------------ | -------------------------------------------------------------------------- | ------- |
| `serviceAccount.create`        | Create a ServiceAccount                                                     | `true`  |
| `serviceAccount.name`          | ServiceAccount name (generated from fullname when empty)                    | `""`    |
| `serviceAccount.annotations`   | ServiceAccount annotations (e.g. IRSA `eks.amazonaws.com/role-arn`)         | `{}`    |
| `automountServiceAccountToken` | Mount the SA token (auto-enabled when an IRSA role-arn annotation is set)   | `false` |

### Security context parameters

| Name                                              | Description                                | Value            |
| ------------------------------------------------- | ------------------------------------------ | ---------------- |
| `podSecurityContext.runAsNonRoot`                 | Run the pod as non-root                    | `true`           |
| `podSecurityContext.runAsUser`                    | Pod user ID                                | `65532`          |
| `podSecurityContext.runAsGroup`                   | Pod group ID                               | `65532`          |
| `podSecurityContext.fsGroup`                      | Volume ownership group                     | `65532`          |
| `podSecurityContext.fsGroupChangePolicy`          | When to apply fsGroup ownership            | `OnRootMismatch` |
| `podSecurityContext.seccompProfile.type`          | Pod seccomp profile                        | `RuntimeDefault` |
| `containerSecurityContext.runAsNonRoot`           | Run the container as non-root              | `true`           |
| `containerSecurityContext.runAsUser`              | Container user ID                          | `65532`          |
| `containerSecurityContext.runAsGroup`             | Container group ID                         | `65532`          |
| `containerSecurityContext.readOnlyRootFilesystem` | Mount the root filesystem read-only        | `true`           |
| `containerSecurityContext.allowPrivilegeEscalation`| Allow privilege escalation                | `false`          |
| `containerSecurityContext.seccompProfile.type`    | Container seccomp profile                  | `RuntimeDefault` |
| `containerSecurityContext.capabilities.drop`      | Dropped capabilities                       | `["ALL"]`        |
| `initChown.enabled`                               | Run a root init container to chown the cache | `false`        |
| `initChown.image`                                 | Init container image                       | `busybox:1.37`   |

### Probe parameters

| Name                                | Description                          | Value      |
| ----------------------------------- | ------------------------------------ | ---------- |
| `livenessProbe.httpGet.path`        | Liveness probe path                  | `/healthz` |
| `livenessProbe.httpGet.port`        | Liveness probe port                  | `admin`    |
| `livenessProbe.initialDelaySeconds` | Liveness initial delay               | `5`        |
| `livenessProbe.periodSeconds`       | Liveness period                      | `10`       |
| `livenessProbe.timeoutSeconds`      | Liveness timeout                     | `2`        |
| `livenessProbe.failureThreshold`    | Liveness failure threshold           | `3`        |
| `readinessProbe.httpGet.path`       | Readiness probe path                 | `/readyz`  |
| `readinessProbe.httpGet.port`       | Readiness probe port                 | `admin`    |
| `readinessProbe.initialDelaySeconds`| Readiness initial delay              | `3`        |
| `readinessProbe.periodSeconds`      | Readiness period                     | `10`       |
| `readinessProbe.timeoutSeconds`     | Readiness timeout                    | `2`        |
| `readinessProbe.failureThreshold`   | Readiness failure threshold          | `3`        |
| `startupProbe.enabled`              | Enable the startup probe             | `true`     |
| `startupProbe.httpGet.path`         | Startup probe path                   | `/healthz` |
| `startupProbe.httpGet.port`         | Startup probe port                   | `admin`    |
| `startupProbe.periodSeconds`        | Startup period                       | `5`        |
| `startupProbe.failureThreshold`     | Startup failure threshold            | `30`       |

### Resources and Go runtime parameters

| Name                        | Description                                                          | Value   |
| --------------------------- | ------------------------------------------------------------------- | ------- |
| `resources.requests.cpu`    | CPU request                                                         | `500m`  |
| `resources.requests.memory` | Memory request                                                      | `256Mi` |
| `resources.limits.memory`   | Memory limit (no CPU limit so compression bursts aren't throttled)  | `1Gi`   |
| `goRuntime.memLimit`        | Explicit `GOMEMLIMIT` (overrides the auto-derived value)            | `""`    |
| `goRuntime.memLimitAuto`    | Derive `GOMEMLIMIT` as 90% of `resources.limits.memory`             | `true`  |
| `goRuntime.maxProcs`        | `GOMAXPROCS` (empty = Go default)                                   | `""`    |

### Scheduling parameters

| Name                            | Description                                  | Value |
| ------------------------------- | -------------------------------------------- | ----- |
| `nodeSelector`                  | Node selector                                | `{}`  |
| `tolerations`                   | Tolerations                                  | `[]`  |
| `affinity`                      | Affinity rules                               | `{}`  |
| `topologySpreadConstraints`     | Topology spread constraints                  | `[]`  |
| `priorityClassName`             | PriorityClass name                           | `""`  |
| `terminationGracePeriodSeconds` | Termination grace period                     | `30`  |
| `podAnnotations`                | Extra pod annotations                        | `{}`  |
| `podLabels`                     | Extra pod labels                             | `{}`  |

### Autoscaling and disruption parameters

| Name                                            | Description                          | Value   |
| ----------------------------------------------- | ------------------------------------ | ------- |
| `autoscaling.enabled`                           | Enable the HorizontalPodAutoscaler   | `false` |
| `autoscaling.minReplicas`                       | Minimum replicas                     | `1`     |
| `autoscaling.maxReplicas`                       | Maximum replicas                     | `5`     |
| `autoscaling.targetCPUUtilizationPercentage`    | Target CPU utilization               | `80`    |
| `podDisruptionBudget.enabled`                   | Enable the PodDisruptionBudget       | `false` |
| `podDisruptionBudget.minAvailable`              | Minimum available pods               | `1`     |

### NetworkPolicy parameters

| Name                           | Description                                                         | Value   |
| ------------------------------ | ----------------------------------------------------------------- | ------- |
| `networkPolicy.enabled`        | Restrict ingress to the pods (egress left open)                    | `false` |
| `networkPolicy.publicIngress`  | Peers allowed to reach the public port (empty = anywhere)          | `[]`    |
| `networkPolicy.adminIngress`   | Peers allowed to reach the admin port (empty = same namespace)     | `[]`    |

### Metrics parameters

| Name                                | Description                                                       | Value      |
| ----------------------------------- | ---------------------------------------------------------------- | ---------- |
| `serviceMonitor.enabled`            | Create a Prometheus Operator ServiceMonitor (auto-provisions admin Svc) | `false`    |
| `serviceMonitor.path`               | Scrape path on the admin listener                               | `/metrics` |
| `serviceMonitor.scheme`             | Scrape scheme                                                   | `http`     |
| `serviceMonitor.interval`           | Scrape interval                                                 | `30s`      |
| `serviceMonitor.scrapeTimeout`      | Per-scrape timeout                                              | `10s`      |
| `serviceMonitor.honorLabels`        | Preserve target-exposed label values on collision              | `false`    |
| `serviceMonitor.labels`             | Extra labels (e.g. to match your Prometheus selector)          | `{}`       |
| `serviceMonitor.namespace`          | Namespace for the ServiceMonitor (defaults to release ns)      | `""`       |
| `serviceMonitor.namespaceSelector`  | Limit which namespaces Prometheus searches for the Service     | `{}`       |
| `serviceMonitor.relabelings`        | Relabeling rules applied before the scrape                     | `[]`       |
| `serviceMonitor.metricRelabelings`  | Relabeling rules applied to scraped samples                    | `[]`       |

## Configuration and installation details

### AWS authentication

IRSA is the default: annotate the ServiceAccount with `serviceAccount.annotations."eks.amazonaws.com/role-arn"` and no Secret is mounted (`automountServiceAccountToken` is auto-enabled). Alternatively, pre-create a Secret with an AWS shared-credentials file and set `aws.credentials.existingSecret`.

### Sentry token

When `sentry.apiUrl` is set, `sentry.org`, `sentry.project`, and a token source are required. The token is mounted as a file and wired via `SENTRY_AUTH_TOKEN_FILE`. Prefer `sentry.token.existingSecret`; `sentry.token.create=true` stores the value in a chart-managed Secret (visible via `helm get values`).

### Cache volume ownership

`podSecurityContext.fsGroup` makes the kubelet group-own the cache PVC for the nonroot daemon. If your storage class ignores `fsGroup` (some hostPath/NFS CSI drivers), `/readyz` returns 503 with a cache permission error — set `initChown.enabled=true`.

### Security

The workload is Pod Security Standards **restricted**-compliant. Set `networkPolicy.enabled=true` to restrict ingress, and pin `image.tag` to an immutable `sha-<12>` tag (or a digest) in production.

### Metrics

The admin `/metrics` endpoint serves the Prometheus exposition format via `client_golang`. Set `serviceMonitor.enabled=true` (Prometheus Operator) to scrape it; this also provisions the admin Service.
