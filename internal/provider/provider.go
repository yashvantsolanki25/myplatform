// Package provider hides cloud-specific cluster discovery and authentication
// behind one interface, so the rest of the CLI never imports a cloud SDK.
package provider

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/client-go/rest"
)

// Cluster is the cloud-neutral view of a managed Kubernetes cluster.
type Cluster struct {
	Provider string // aws | gcp | azure
	Name     string
	Region   string
	Version  string
	Status   string
	// Meta holds provider-specific data needed to connect
	// (endpoint, CA, resource group, ...).
	Meta map[string]string
}

// Ref is the unique "provider/name" reference users pass to --cluster.
func (c Cluster) Ref() string { return c.Provider + "/" + c.Name }

// Provider discovers clusters and builds authenticated Kubernetes configs.
type Provider interface {
	Name() string
	// ListClusters may return clusters AND a non-nil error when only some
	// regions/locations failed (partial results are still useful).
	ListClusters(ctx context.Context) ([]Cluster, error)
	RESTConfig(ctx context.Context, c Cluster) (*rest.Config, error)
}

// Options selects which clouds are enabled.
type Options struct {
	AWSRegions        []string
	GCPProject        string
	AzureSubscription string
}

// Build constructs a provider for every cloud that was configured.
func Build(ctx context.Context, o Options) ([]Provider, error) {
	var out []Provider
	var errs []error

	if len(o.AWSRegions) > 0 {
		p, err := NewAWS(ctx, o.AWSRegions)
		if err != nil {
			errs = append(errs, fmt.Errorf("aws: %w", err))
		} else {
			out = append(out, p)
		}
	}
	if o.GCPProject != "" {
		p, err := NewGCP(ctx, o.GCPProject)
		if err != nil {
			errs = append(errs, fmt.Errorf("gcp: %w", err))
		} else {
			out = append(out, p)
		}
	}
	if o.AzureSubscription != "" {
		p, err := NewAzure(o.AzureSubscription)
		if err != nil {
			errs = append(errs, fmt.Errorf("azure: %w", err))
		} else {
			out = append(out, p)
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	if len(out) == 0 {
		return nil, errors.New("no cloud configured: set --aws-region, --gcp-project and/or --azure-subscription")
	}
	return out, nil
}
