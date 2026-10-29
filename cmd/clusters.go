package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var clustersCmd = &cobra.Command{
	Use:   "clusters",
	Short: "Manage target clusters",
}

var clustersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Kubernetes clusters across all configured clouds",
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		provs, err := buildProviders(ctx)
		if err != nil {
			return err
		}
		all := discover(ctx, provs)
		sort.Slice(all, func(i, j int) bool { return all[i].c.Ref() < all[j].c.Ref() })

		w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
		fmt.Fprintln(w, "REF\tREGION\tVERSION\tSTATUS")
		for _, t := range all {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.c.Ref(), t.c.Region, t.c.Version, t.c.Status)
		}
		return w.Flush()
	},
}

func init() { clustersCmd.AddCommand(clustersListCmd) }
