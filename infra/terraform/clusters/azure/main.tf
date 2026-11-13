resource "azurerm_resource_group" "cluster" {
  name     = "${var.cluster_name}-rg"
  location = var.location
}

resource "azurerm_virtual_network" "cluster" {
  name                = "${var.cluster_name}-network"
  address_space       = ["10.40.0.0/16"]
  location            = azurerm_resource_group.cluster.location
  resource_group_name = azurerm_resource_group.cluster.name
}

resource "azurerm_subnet" "cluster" {
  name                 = "${var.cluster_name}-nodes"
  resource_group_name  = azurerm_resource_group.cluster.name
  virtual_network_name = azurerm_virtual_network.cluster.name
  address_prefixes     = ["10.40.0.0/20"]
}

resource "azurerm_user_assigned_identity" "cluster" {
  name                = "${var.cluster_name}-identity"
  location            = azurerm_resource_group.cluster.location
  resource_group_name = azurerm_resource_group.cluster.name
}

resource "azurerm_role_assignment" "network_contributor" {
  scope                = azurerm_subnet.cluster.id
  role_definition_name = "Network Contributor"
  principal_id         = azurerm_user_assigned_identity.cluster.principal_id
}

resource "azurerm_kubernetes_cluster" "cluster" {
  name                = var.cluster_name
  location            = azurerm_resource_group.cluster.location
  resource_group_name = azurerm_resource_group.cluster.name
  dns_prefix          = var.cluster_name
  kubernetes_version  = var.kubernetes_version

  default_node_pool {
    name                         = "system"
    vm_size                      = "Standard_D2s_v5"
    vnet_subnet_id               = azurerm_subnet.cluster.id
    auto_scaling_enabled         = true
    min_count                    = 1
    max_count                    = 3
    node_count                   = 2
    only_critical_addons_enabled = true
  }

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.cluster.id]
  }

  role_based_access_control_enabled = true

  api_server_access_profile {
    authorized_ip_ranges = var.admin_ipv4_cidrs
  }

  network_profile {
    network_plugin    = "azure"
    load_balancer_sku = "standard"
    service_cidr      = "10.41.0.0/16"
    dns_service_ip    = "10.41.0.10"
  }

  depends_on = [azurerm_role_assignment.network_contributor]
}

resource "azurerm_kubernetes_cluster_node_pool" "apps" {
  name                  = "apps"
  kubernetes_cluster_id = azurerm_kubernetes_cluster.cluster.id
  vm_size               = "Standard_D2s_v5"
  vnet_subnet_id        = azurerm_subnet.cluster.id
  mode                  = "User"
  auto_scaling_enabled  = true
  min_count             = 1
  max_count             = 3
  node_count            = 2
  orchestrator_version  = var.kubernetes_version
}
