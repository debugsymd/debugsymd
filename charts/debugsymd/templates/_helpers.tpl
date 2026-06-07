{{/*
Expand the name of the chart.
*/}}
{{- define "debugsymd.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name.
*/}}
{{- define "debugsymd.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Chart name and version, for the helm.sh/chart label.
*/}}
{{- define "debugsymd.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Headless governing service name for the StatefulSet.
*/}}
{{- define "debugsymd.headlessName" -}}
{{- printf "%s-headless" (include "debugsymd.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Resolved image reference.
*/}}
{{- define "debugsymd.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{/*
Selector labels — the immutable subset used in StatefulSet/Service selectors.
Never add version/tag here: StatefulSet spec.selector is immutable.
*/}}
{{- define "debugsymd.selectorLabels" -}}
app.kubernetes.io/name: {{ include "debugsymd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: server
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "debugsymd.labels" -}}
helm.sh/chart: {{ include "debugsymd.chart" . }}
{{ include "debugsymd.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Admin Service labels — carry component=admin so a ServiceMonitor can target the
admin Service specifically (the public/headless Services share component=server).
*/}}
{{- define "debugsymd.adminLabels" -}}
helm.sh/chart: {{ include "debugsymd.chart" . }}
{{ include "debugsymd.adminSelectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Admin Service selector labels — the distinguishing label set a ServiceMonitor
matches on. Note: this is NOT the admin Service's pod selector (pods are
component=server); it identifies the admin Service object itself.
*/}}
{{- define "debugsymd.adminSelectorLabels" -}}
app.kubernetes.io/name: {{ include "debugsymd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: admin
{{- end -}}

{{/*
Whether the admin Service should be rendered (explicitly enabled or required by
the ServiceMonitor as its scrape target).
*/}}
{{- define "debugsymd.adminServiceEnabled" -}}
{{- if or .Values.adminService.enabled .Values.serviceMonitor.enabled -}}true{{- end -}}
{{- end -}}

{{/*
Effective automountServiceAccountToken: honor an explicit true, otherwise
auto-enable when an IRSA role-arn annotation is present (the projected
web-identity token requires the SA token volume). Defaults to false otherwise.
*/}}
{{- define "debugsymd.automountToken" -}}
{{- if .Values.automountServiceAccountToken -}}
true
{{- else if index .Values.serviceAccount.annotations "eks.amazonaws.com/role-arn" -}}
true
{{- else -}}
false
{{- end -}}
{{- end -}}

{{/*
ServiceAccount name to use.
*/}}
{{- define "debugsymd.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "debugsymd.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Resolved Sentry token Secret name (empty when no token source is configured).
*/}}
{{- define "debugsymd.sentrySecretName" -}}
{{- if .Values.sentry.token.existingSecret -}}
{{- .Values.sentry.token.existingSecret -}}
{{- else if .Values.sentry.token.create -}}
{{- printf "%s-sentry" (include "debugsymd.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Effective AWS region: aws.region, falling back to s3.region.
*/}}
{{- define "debugsymd.awsRegion" -}}
{{- default .Values.s3.region .Values.aws.region -}}
{{- end -}}

{{/*
Convert a Kubernetes memory quantity (e.g. "1Gi", "512Mi", "1000M") to bytes.
Returns an empty string for unrecognized/fractional inputs.
*/}}
{{- define "debugsymd.quantityBytes" -}}
{{- $q := toString . -}}
{{- $num := $q | regexFind "^[0-9]+" -}}
{{- $frac := $q | regexFind "^[0-9]+\\." -}}
{{- if and $num (not $frac) -}}
{{- $suffix := $q | regexFind "[A-Za-z]+$" -}}
{{- $mult := int64 1 -}}
{{- if eq $suffix "Ki" -}}{{- $mult = int64 1024 -}}
{{- else if eq $suffix "Mi" -}}{{- $mult = int64 1048576 -}}
{{- else if eq $suffix "Gi" -}}{{- $mult = int64 1073741824 -}}
{{- else if eq $suffix "Ti" -}}{{- $mult = int64 1099511627776 -}}
{{- else if eq $suffix "k" -}}{{- $mult = int64 1000 -}}
{{- else if eq $suffix "M" -}}{{- $mult = int64 1000000 -}}
{{- else if eq $suffix "G" -}}{{- $mult = int64 1000000000 -}}
{{- else if eq $suffix "T" -}}{{- $mult = int64 1000000000000 -}}
{{- end -}}
{{- mul (int64 $num) $mult -}}
{{- end -}}
{{- end -}}

{{/*
Resolve GOMEMLIMIT (in bytes, as a string). Explicit goRuntime.memLimit wins;
otherwise derive 90% of resources.limits.memory when memLimitAuto is true.
Empty result means GOMEMLIMIT should not be set.
*/}}
{{- define "debugsymd.goMemLimit" -}}
{{- if .Values.goRuntime.memLimit -}}
{{- .Values.goRuntime.memLimit -}}
{{- else if .Values.goRuntime.memLimitAuto -}}
{{- $limit := dig "limits" "memory" "" .Values.resources -}}
{{- if $limit -}}
{{- $bytes := include "debugsymd.quantityBytes" $limit -}}
{{- if $bytes -}}
{{- div (mul (int64 $bytes) 90) 100 -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Validate cross-field constraints. Mirrors config.validate() so failures surface
at `helm install` time instead of as a CrashLoopBackOff.
*/}}
{{- define "debugsymd.validate" -}}
{{- if not .Values.s3.bucket -}}
{{- fail "s3.bucket is required" -}}
{{- end -}}
{{- if .Values.sentry.apiUrl -}}
{{- if not .Values.sentry.org -}}
{{- fail "sentry.org is required when sentry.apiUrl is set" -}}
{{- end -}}
{{- if not .Values.sentry.project -}}
{{- fail "sentry.project is required when sentry.apiUrl is set" -}}
{{- end -}}
{{- if not (or .Values.sentry.token.create .Values.sentry.token.existingSecret) -}}
{{- fail "sentry.apiUrl is set but no Sentry token configured: set sentry.token.create+value or sentry.token.existingSecret" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Daemon arguments, rendered from values. Conditional flags use `with` so empty
values are omitted entirely.
*/}}
{{- define "debugsymd.args" -}}
- -bind={{ .Values.bind }}
- -admin={{ .Values.admin }}
- -cache-dir={{ .Values.cache.dir }}
- -cache-max-unused-for={{ .Values.cache.maxUnusedFor }}
- -s3-bucket={{ required "s3.bucket is required" .Values.s3.bucket }}
{{- with .Values.s3.region }}
- -s3-region={{ . }}
{{- end }}
{{- with .Values.s3.endpointUrl }}
- -s3-endpoint-url={{ . }}
{{- end }}
{{- with .Values.s3.keyPrefix }}
- -s3-key-prefix={{ . }}
{{- end }}
{{- with .Values.sentry.apiUrl }}
- -sentry-api-url={{ . }}
- -sentry-org={{ required "sentry.org is required when sentry.apiUrl is set" $.Values.sentry.org }}
- -sentry-project={{ required "sentry.project is required when sentry.apiUrl is set" $.Values.sentry.project }}
{{- end }}
- -log-level={{ .Values.logLevel }}
- -debuginfod={{ .Values.debuginfod }}
{{- end -}}
