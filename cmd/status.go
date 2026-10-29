package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"

	"github.com/example/myplatform/internal/deploy"
)

var statusFlags struct {
	name, namespace string
	clusters        []string
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show per-cluster rollout status for an app",
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		provs, err := buildProviders(ctx)
		if err != nil {
			return err
		}
		all := discover(ctx, provs)
		targets := all
		if len(statusFlags.clusters) > 0 {
			if targets, err = resolve(all, statusFlags.clusters); err != nil {
				return err
			}
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
		fmt.Fprintln(w, "CLUSTER\tREADY\tUP-TO-DATE\tDETAIL")
		for _, t := range targets {
			cfg, err := t.p.RESTConfig(ctx, t.c)
			if err != nil {
				fmt.Fprintf(w, "%s\t-\t-\tauth: %v\n", t.c.Ref(), err)
				continue
			}
			cs, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				fmt.Fprintf(w, "%s\t-\t-\t%v\n", t.c.Ref(), err)
				continue
			}
			st, err := deploy.GetStatus(ctx, cs, statusFlags.namespace, statusFlags.name)
			if err != nil {
				fmt.Fprintf(w, "%s\t-\t-\t%v\n", t.c.Ref(), err)
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%d/%d\t\n", t.c.Ref(), st, st.Updated, st.Desired)
		}
		return w.Flush()
	},
}

func init() {
	f := statusCmd.Flags()
	f.StringVar(&statusFlags.name, "name", "", "app name (required)")
	f.StringVarP(&statusFlags.namespace, "namespace", "n", "default", "namespace")
	f.StringSliceVar(&statusFlags.clusters, "cluster", nil, "limit to these clusters (default: all discovered)")
	_ = statusCmd.MarkFlagRequired("name")
}
