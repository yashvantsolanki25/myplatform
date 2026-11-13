output "monitoring_namespace" {
  description = "Namespace containing the monitoring stack."
  value       = helm_release.kube_prometheus_stack.namespace
}

output "grafana_access_command" {
  description = "Port-forward command for private Grafana access."
  value       = "kubectl -n ${var.monitoring_namespace} port-forward svc/myplatform-monitoring-grafana 3000:80"
}
