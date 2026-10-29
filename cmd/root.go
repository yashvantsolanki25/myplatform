package cmd

import (
	"context"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/example/myplatform/internal/provider"
)

var opts struct {
	awsRegions []string
	gcpProject string
	azureSub   string
}

var rootCmd = &cobra.Command{
	Use:           "myplatform",
	Short:         "Deploy apps to Kubernetes clusters on AWS, GCP and Azure",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	f := rootCmd.PersistentFlags()
	f.StringSliceVar(&opts.awsRegions, "aws-region", envList("MYPLATFORM_AWS_REGIONS", "AWS_REGION"),
		"AWS regions to scan for EKS clusters (comma-separated)")
	f.StringVar(&opts.gcpProject, "gcp-project", envFirst("GOOGLE_CLOUD_PROJECT", "GCP_PROJECT"),
		"GCP project to scan for GKE clusters")
	f.StringVar(&opts.azureSub, "azure-subscription", envFirst("AZURE_SUBSCRIPTION_ID"),
		"Azure subscription to scan for AKS clusters")

	rootCmd.AddCommand(clustersCmd, deployCmd, statusCmd)
}

func buildProviders(ctx context.Context) ([]provider.Provider, error) {
	return provider.Build(ctx, provider.Options{
		AWSRegions:        opts.awsRegions,
		GCPProject:        opts.gcpProject,
		AzureSubscription: opts.azureSub,
	})
}

func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func envList(keys ...string) []string {
	v := envFirst(keys...)
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}
