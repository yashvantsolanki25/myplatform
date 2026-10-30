package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"k8s.io/client-go/rest"
)

// GCP discovers GKE clusters using Application Default Credentials.
type GCP struct {
	project string
	ts      oauth2.TokenSource
}

func NewGCP(ctx context.Context, project string, creds *GCPCredentials) (*GCP, error) {
	if creds != nil && creds.ServiceAccountJSON != "" {
		conf, err := google.JWTConfigFromJSON([]byte(creds.ServiceAccountJSON),
			"https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return nil, fmt.Errorf("parse service account key: %w", err)
		}
		return &GCP{project: project, ts: conf.TokenSource(ctx)}, nil
	}
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, err
	}
	return &GCP{project: project, ts: ts}, nil
}

func (g *GCP) Name() string { return "gcp" }

func (g *GCP) ListClusters(ctx context.Context) ([]Cluster, error) {
	client, err := container.NewClusterManagerClient(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	// "-" lists clusters across every zone and region in the project.
	resp, err := client.ListClusters(ctx, &containerpb.ListClustersRequest{
		Parent: fmt.Sprintf("projects/%s/locations/-", g.project),
	})
	if err != nil {
		return nil, err
	}

	var out []Cluster
	for _, c := range resp.Clusters {
		out = append(out, Cluster{
			Provider: "gcp",
			Name:     c.Name,
			Region:   c.Location,
			Version:  c.CurrentMasterVersion,
			Status:   c.Status.String(),
			Meta: map[string]string{
				"endpoint": c.Endpoint,
				"ca":       c.GetMasterAuth().GetClusterCaCertificate(),
			},
		})
	}
	if len(resp.MissingZones) > 0 {
		return out, fmt.Errorf("some zones were unreachable: %v", resp.MissingZones)
	}
	return out, nil
}

func (g *GCP) RESTConfig(_ context.Context, c Cluster) (*rest.Config, error) {
	ca, err := base64.StdEncoding.DecodeString(c.Meta["ca"])
	if err != nil {
		return nil, fmt.Errorf("decode CA: %w", err)
	}
	return &rest.Config{
		Host:            "https://" + c.Meta["endpoint"],
		TLSClientConfig: rest.TLSClientConfig{CAData: ca},
		// oauth2.Transport refreshes the access token automatically.
		WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
			return &oauth2.Transport{Source: g.ts, Base: rt}
		},
	}, nil
}
