# Infrastructure, deployment, and monitoring

The web UI discovers existing EKS, GKE, and AKS clusters from the cloud
credentials you configure. Select one or more providers' clusters in the Deploy
form. Public LoadBalancer exposure returns the cloud-assigned HTTP URL; choose
ClusterIP when the workload must stay cluster-internal. Public load balancers
can incur charges, and cluster networking must allow traffic to the container
port.

## Provision a cluster and monitoring stack

Terraform configurations are provided for EKS, GKE, and AKS. Ansible
orchestrates the selected cloud's Terraform configuration, configures local
`kubectl` credentials, and installs Prometheus and Grafana. Provisioning is
explicitly gated because it creates billable cloud resources. Each cluster API
endpoint is restricted to the IPv4 CIDRs you supply; `0.0.0.0/0` is rejected.

Requirements: Terraform 1.6+, Ansible, kubectl, and the corresponding cloud CLI
(`aws`, `gcloud`, or `az`) authenticated with permissions to create the network,
cluster, and node resources. Also provide a provider region, and a restricted
public IPv4 CIDR that includes both the operator and myplatform server egress
addresses. EKS and AKS also require a Kubernetes version supported in that
region; GKE follows its Regular release channel.
For GCP, authenticate both the `gcloud` CLI and Application Default
Credentials (`gcloud auth login` and `gcloud auth application-default login`).

Example for AWS (replace the CIDR with your own public IP in CIDR notation):

```sh
export TF_VAR_cluster_name=myplatform
export TF_VAR_region=us-east-1
export TF_VAR_kubernetes_version=1.33
export TF_VAR_admin_ipv4_cidrs='["203.0.113.7/32"]'
export TF_VAR_grafana_admin_password="$(openssl rand -base64 24)"
ansible-playbook infra/ansible/provision-cluster.yml \
  -e cloud_provider=aws
```

For GCP, set `TF_VAR_project_id`, `TF_VAR_region`, and
`TF_VAR_admin_ipv4_cidrs`, then pass `-e cloud_provider=gcp`. For Azure, set
`TF_VAR_subscription_id`, `TF_VAR_location`, `TF_VAR_kubernetes_version`, and
`TF_VAR_admin_ipv4_cidrs`, then pass `-e cloud_provider=azure`. All providers
accept optional `TF_VAR_cluster_name` (default `myplatform`). The playbook
prints each Terraform plan and requires you to type a confirmation before
applying it. Cluster creation, nodes, NAT gateways, and load balancers may all
incur charges.

The playbook initializes and applies the selected cluster plan, configures the
local kubeconfig using the cloud CLI, then installs monitoring. Afterwards,
configure the matching cloud credentials in myplatform and refresh the cluster
list to deploy applications.

## Install monitoring on an existing cluster

Use this when the target cluster already exists and is selected in kubeconfig:

```sh
export KUBECONFIG="$HOME/.kube/config"
export TF_VAR_grafana_admin_password="$(openssl rand -base64 24)"
ansible-playbook infra/ansible/site.yml
```

Terraform installs the Prometheus Community
`kube-prometheus-stack` Helm chart. Prometheus, Grafana, and Alertmanager
services remain private ClusterIP services. The stack is accessed using
`kubectl port-forward`, not an unauthenticated public dashboard:

```sh
kubectl -n monitoring port-forward svc/myplatform-monitoring-grafana 3000:80
```

Open <http://localhost:3000> and sign in as `admin` using the value of
`TF_VAR_grafana_admin_password`.

Terraform state and plan files can contain credentials. They are git-ignored,
but local state is not encrypted by default; use a secured remote state backend
for team or production use, and restrict access to the Grafana password.

To remove a provisioned cluster, first destroy the monitoring stack while the
cluster is still available, then destroy its provider resources. Review each
Terraform destroy plan and confirm it interactively:

```sh
export TF_VAR_grafana_admin_password="$(openssl rand -base64 24)"
terraform -chdir=infra/terraform destroy \
  -var "kubeconfig_path=${KUBECONFIG:-$HOME/.kube/config}"
terraform -chdir=infra/terraform/clusters/aws destroy
```

Use the matching `gcp` or `azure` directory for clusters created on those
providers. Keep Terraform state until all resources have been destroyed.
