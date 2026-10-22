{{- define "banda.publicUrl" -}}
{{- if .Values.publicUrl -}}{{ .Values.publicUrl }}
{{- else if and .Values.ingress.enabled .Values.ingress.host -}}{{ ternary "https" "http" (ne .Values.ingress.tlsSecret "") }}://{{ .Values.ingress.host }}
{{- else -}}http://localhost:8787
{{- end -}}
{{- end -}}

{{- define "banda.previewUrl" -}}
{{- if .Values.previewUrl -}}{{ .Values.previewUrl }}
{{- else if and .Values.ingress.enabled .Values.ingress.previewHost -}}{{ ternary "https" "http" (ne .Values.ingress.tlsSecret "") }}://{{ .Values.ingress.previewHost }}
{{- end -}}
{{- end -}}

{{- define "banda.issuer" -}}
{{- if .Values.oidc.issuer -}}{{ .Values.oidc.issuer }}
{{- else if .Values.dex.enabled -}}{{ include "banda.publicUrl" . }}/dex
{{- end -}}
{{- end -}}

{{- define "banda.secret" -}}{{ .Values.api.existingSecret | default "banda-api" }}{{- end -}}

{{- define "banda.image" -}}{{ .image.repository }}:{{ .image.tag | default .root.Chart.AppVersion }}{{- end -}}

{{- define "banda.labels" -}}
app.kubernetes.io/part-of: banda
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}
