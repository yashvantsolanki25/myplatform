# myplatform

CLI that deploys an app to Kubernetes clusters on AWS (EKS), Google Cloud (GKE)
and Azure (AKS) in parallel, using each cloud's Go SDK for discovery and auth.

## Build

    go mod tidy        # resolves SDK versions (needs network)
    go build -o myplatform .

## Authenticate (standard SDK credential chains, no extra tooling)

| Cloud | How |
|-------|-----|
| AWS   | `aws configure` / SSO / env vars / IRSA |
| GCP   | `gcloud auth application-default login` |
| Azure | `az login` or service principal env vars |

## Use

    export MYPLATFORM_AWS_REGIONS=eu-west-1,us-east-1
    export GOOGLE_CLOUD_PROJECT=my-project
    export AZURE_SUBSCRIPTION_ID=00000000-0000-0000-0000-000000000000

    myplatform clusters list

    myplatform deploy --name shop --image nginx:1.27 --port 80 --replicas 2 \
      --cluster aws/prod-eks --cluster gcp/prod-gke --cluster azure/prod-aks \
      -e LOG_LEVEL=info

    myplatform status --name shop

## Layout

    main.go
    cmd/                 cobra commands (clusters, deploy, status)
    internal/provider/   Provider interface + aws.go, gcp.go, azure.go
    internal/deploy/     client-go apply + rollout wait (cloud-agnostic)

## Adding a cloud
Implement `provider.Provider` (`Name`, `ListClusters`, `RESTConfig`) and
register it in `provider.Build`. Nothing else changes.
