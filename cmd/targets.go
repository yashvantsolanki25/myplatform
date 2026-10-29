package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/example/myplatform/internal/provider"
)

// target pairs a cluster with the provider able to authenticate to it.
type target struct {
	p provider.Provider
	c provider.Cluster
}

// discover lists clusters from every provider. A failing provider only
// produces a warning so one broken cloud never blocks the others.
func discover(ctx context.Context, provs []provider.Provider) []target {
	var out []target
	for _, p := range provs {
		cs, err := p.ListClusters(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", p.Name(), err)
		}
		for _, c := range cs {
			out = append(out, target{p: p, c: c})
		}
	}
	return out
}

// resolve maps user-supplied references ("aws/prod-eks" or just "prod-eks")
// to discovered clusters, rejecting unknown or ambiguous names.
func resolve(all []target, refs []string) ([]target, error) {
	var out []target
	for _, ref := range refs {
		var matches []target
		for _, t := range all {
			if t.c.Ref() == ref || t.c.Name == ref {
				matches = append(matches, t)
			}
		}
		switch len(matches) {
		case 0:
			return nil, fmt.Errorf("cluster %q not found (run `myplatform clusters list`)", ref)
		case 1:
			out = append(out, matches[0])
		default:
			return nil, fmt.Errorf("cluster name %q is ambiguous; use provider/name", ref)
		}
	}
	return out, nil
}
