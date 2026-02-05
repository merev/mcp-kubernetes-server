{{- define "mcp.name" -}}
mcp-kubernetes-server
{{- end }}

{{- define "mcp.fullname" -}}
{{- printf "%s-%s" (include "mcp.name" .) .Release.Name -}}
{{- end }}
