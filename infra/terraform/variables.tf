variable "kubeconfig_path" {
  description = "Path to kubeconfig for the target cluster."
  type        = string
}

variable "monitoring_namespace" {
  description = "Namespace for Prometheus, Grafana, and supporting components."
  type        = string
  default     = "monitoring"
}

variable "chart_version" {
  description = "Pinned kube-prometheus-stack chart version."
  type        = string
  default     = "70.4.2"
}

variable "grafana_admin_password" {
  description = "Initial Grafana administrator password; supply via TF_VAR_grafana_admin_password."
  type        = string
  sensitive   = true

  validation {
    condition     = length(var.grafana_admin_password) >= 12
    error_message = "Grafana admin password must be at least 12 characters."
  }
}
