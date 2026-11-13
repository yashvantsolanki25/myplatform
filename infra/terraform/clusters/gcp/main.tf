resource "google_compute_network" "cluster" {
  name                    = "${var.cluster_name}-network"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "cluster" {
  name                     = "${var.cluster_name}-subnet"
  region                   = var.region
  network                  = google_compute_network.cluster.id
  ip_cidr_range            = "10.10.0.0/20"
  private_ip_google_access = true

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = "10.20.0.0/16"
  }
  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = "10.30.0.0/20"
  }
}

resource "google_container_cluster" "cluster" {
  name                     = var.cluster_name
  location                 = var.region
  network                  = google_compute_network.cluster.name
  subnetwork               = google_compute_subnetwork.cluster.name
  networking_mode          = "VPC_NATIVE"
  remove_default_node_pool = true
  initial_node_count       = 1
  deletion_protection      = false

  release_channel {
    channel = "REGULAR"
  }

  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }

  master_authorized_networks_config {
    dynamic "cidr_blocks" {
      for_each = var.admin_ipv4_cidrs
      content {
        cidr_block   = cidr_blocks.value
        display_name = "myplatform-admin-${cidr_blocks.key}"
      }
    }
  }
}

resource "google_container_node_pool" "default" {
  name       = "${var.cluster_name}-default"
  location   = var.region
  cluster    = google_container_cluster.cluster.name
  node_count = 1

  autoscaling {
    min_node_count = 1
    max_node_count = 3
  }

  management {
    auto_repair  = true
    auto_upgrade = true
  }

  node_config {
    machine_type = "e2-standard-2"
    oauth_scopes = ["https://www.googleapis.com/auth/cloud-platform"]
  }
}
