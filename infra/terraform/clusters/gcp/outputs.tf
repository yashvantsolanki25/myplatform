output "cluster_name" {
  value = google_container_cluster.cluster.name
}

output "region" {
  value = var.region
}

output "project_id" {
  value = var.project_id
}
