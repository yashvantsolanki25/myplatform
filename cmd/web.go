package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/example/myplatform/internal/web"
)

var webFlags struct {
	addr string
}

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Start the web UI for cluster management and deployment",
	Long: `Start a local web server for managing clusters and deploying apps.
Cloud credentials (AWS, GCP, Azure) are configured per-user from the UI's
settings panel — no flags required at startup. Set MYPLATFORM_MYSQL_DSN to use
MySQL; otherwise the local file-backed store is used.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		var store *web.Store
		var err error
		if dsn := os.Getenv("MYPLATFORM_MYSQL_DSN"); dsn != "" {
			store, err = web.NewMySQLStore(ctx, dsn)
		} else {
			store, err = web.NewStore()
		}
		if err != nil {
			return err
		}
		defer store.Close()

		srv := web.NewServer(ctx, web.Config{
			Addr:  webFlags.addr,
			Store: store,
		})
		return srv.ListenAndServe()
	},
}

func init() {
	webCmd.Flags().StringVar(&webFlags.addr, "addr", "localhost:8080", "address to listen on")
	rootCmd.AddCommand(webCmd)
}
