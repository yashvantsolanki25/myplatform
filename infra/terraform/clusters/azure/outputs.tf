output "cluster_name" {
  value = azurerm_kubernetes_cluster.cluster.name
}

output "resource_group_name" {
  value = azurerm_resource_group.cluster.name
}

output "location" {
  value = var.location
}
