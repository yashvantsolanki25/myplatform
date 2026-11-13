resource "kubernetes_namespace_v1" "monitoring" {
  metadata {
    name = var.monitoring_namespace
  }
}

resource "kubernetes_secret_v1" "grafana_admin" {
  metadata {
    name      = "myplatform-grafana-admin"
    namespace = kubernetes_namespace_v1.monitoring.metadata[0].name
  }

  type = "Opaque"
  data = {
    "admin-user"     = "admin"
    "admin-password" = var.grafana_admin_password
  }
}

resource "helm_release" "kube_prometheus_stack" {
  name             = "myplatform-monitoring"
  repository       = "https://prometheus-community.github.io/helm-charts"
  chart            = "kube-prometheus-stack"
  version          = var.chart_version
  namespace        = kubernetes_namespace_v1.monitoring.metadata[0].name
  create_namespace = false
  wait             = true
  timeout          = 900
  depends_on       = [kubernetes_secret_v1.grafana_admin]

  values = [
    yamlencode({
      grafana = {
        admin = {
          existingSecret = kubernetes_secret_v1.grafana_admin.metadata[0].name
        }
        service = {
          type = "ClusterIP"
        }
        ingress = {
          enabled = false
        }
      }
      prometheus = {
        service = {
          type = "ClusterIP"
        }
        ingress = {
          enabled = false
        }
      }
      alertmanager = {
        alertmanager = {
          service = {
            type = "ClusterIP"
          }
        }
      }
    })
  ]
}
