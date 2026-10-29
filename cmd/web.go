package cmd

import (
	"github.com/spf13/cobra"

	"github.com/example/myplatform/internal/provider"
	"github.com/example/myplatform/internal/web"
)

var webFlags struct {
	addr string
}

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Start the web UI for cluster management and deployment",
	Long: `Start a local web server for managing clusters and deploying apps.
Cloud credentials (AWS regions, GCP project, Azure subscription) can be
configured from the UI's settings panel — no flags required at startup.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		srv := web.NewServer(ctx, web.Config{
			Addr: webFlags.addr,
			ProviderOptions: provider.Options{
				AWSRegions:        opts.awsRegions,
				GCPProject:         opts.gcpProject,
				AzureSubscription:  opts.azureSub,
			},
		})
		return srv.ListenAndServe()
	},
}

func init() {
	webCmd.Flags().StringVar(&webFlags.addr, "addr", "localhost:8080", "address to listen on")
	rootCmd.AddCommand(webCmd)
}
