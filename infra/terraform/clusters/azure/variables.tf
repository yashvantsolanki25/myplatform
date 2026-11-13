variable "cluster_name" {
  description = "Name for the AKS cluster."
  type        = string
  default     = "myplatform"
}

variable "subscription_id" {
  description = "Azure subscription ID."
  type        = string
}

variable "location" {
  description = "Azure region for the cluster."
  type        = string
}

variable "kubernetes_version" {
  description = "Kubernetes version supported by AKS in the selected region."
  type        = string
}

variable "admin_ipv4_cidrs" {
  description = "IPv4 CIDRs allowed to reach the public AKS Kubernetes API endpoint."
  type        = list(string)

  validation {
    condition     = length(var.admin_ipv4_cidrs) > 0 && alltrue([for cidr in var.admin_ipv4_cidrs : can(cidrnetmask(cidr)) && cidr != "0.0.0.0/0"])
    error_message = "Provide at least one valid IPv4 CIDR; do not expose the Kubernetes API to 0.0.0.0/0."
  }
}
