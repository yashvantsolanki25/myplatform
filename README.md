# myplatform

CLI that deploys an app to Kubernetes clusters on AWS (EKS), Google Cloud (GKE)
and Azure (AKS) in parallel, using each cloud's Go SDK for discovery and auth.

## Run the web application with Docker

The web application uses MySQL for users and encrypted cloud credentials. The
included Compose setup starts MySQL and the application:

    docker compose --env-file .env.example up --build

Open <http://localhost:8080>, create an account, and add AWS, GCP, or Azure
credentials from **Cloud Credentials**. Account passwords are stored as bcrypt
hashes; cloud credential payloads are AES-256-GCM encrypted before being saved.
The app's `app-data` volume contains the encryption key when
`MYPLATFORM_ENCRYPTION_KEY` is unset, so preserve it together with the MySQL
volume to keep existing credentials readable.

The checked-in `.env.example` contains development-only database passwords.
Before deployment, copy it to `.env`, replace both passwords with strong
hexadecimal values (for example, generate each with `openssl rand -hex 24`),
and set a persistent 64-character hexadecimal encryption key (generate it
with `openssl rand -hex 32`). Do not expose the app
directly to the internet; use HTTPS through a trusted reverse proxy and review
whether open account registration is appropriate for your deployment.

    cp .env.example .env
    # Edit .env, then:
    docker compose up --build -d

To stop the services without deleting data:

    docker compose down

Do not use `docker compose down -v` unless you intend to delete the database
and encryption-key volumes.

The web command uses the file-backed store when `MYPLATFORM_MYSQL_DSN` is not
set. Set that variable to use MySQL outside Docker. `MYPLATFORM_DATA_DIR`
controls where the local store and encryption key are kept.

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
    internal/web/        embedded web UI, authentication, and persistence

## Adding a cloud
Implement `provider.Provider` (`Name`, `ListClusters`, `RESTConfig`) and
register it in `provider.Build`. Nothing else changes.
