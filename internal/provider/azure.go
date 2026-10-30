package provider

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v5"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Well-known AAD application ID of the AKS server; tokens for this audience
// are accepted by AAD-integrated clusters.
const aksServerScope = "6dae42f8-4368-4678-94ff-3960e28e3630/.default"

// Azure discovers AKS clusters using DefaultAzureCredential
// (env vars, managed identity, workload identity, Azure CLI login, ...).
type Azure struct {
	sub  string
	cred azcore.TokenCredential
}

func NewAzure(subscription string, creds *AzureCredentials) (*Azure, error) {
	if creds != nil && creds.ClientID != "" {
		cred, err := azidentity.NewClientSecretCredential(
			creds.TenantID, creds.ClientID, creds.ClientSecret, nil,
		)
		if err != nil {
			return nil, err
		}
		return &Azure{sub: subscription, cred: cred}, nil
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, err
	}
	return &Azure{sub: subscription, cred: cred}, nil
}

func (a *Azure) Name() string { return "azure" }

func (a *Azure) client() (*armcontainerservice.ManagedClustersClient, error) {
	return armcontainerservice.NewManagedClustersClient(a.sub, a.cred, nil)
}

func (a *Azure) ListClusters(ctx context.Context) ([]Cluster, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	var out []Cluster
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return out, err
		}
		for _, mc := range page.Value {
			if mc.ID == nil || mc.Name == nil {
				continue
			}
			rid, err := arm.ParseResourceID(*mc.ID)
			if err != nil {
				continue
			}
			c := Cluster{
				Provider: "azure",
				Name:     *mc.Name,
				Region:   deref(mc.Location),
				Meta:     map[string]string{"resourceGroup": rid.ResourceGroupName},
			}
			if mc.Properties != nil {
				c.Version = deref(mc.Properties.KubernetesVersion)
				c.Status = deref(mc.Properties.ProvisioningState)
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func (a *Azure) RESTConfig(ctx context.Context, c Cluster) (*rest.Config, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	res, err := client.ListClusterUserCredentials(ctx, c.Meta["resourceGroup"], c.Name, nil)
	if err != nil {
		return nil, err
	}
	if len(res.Kubeconfigs) == 0 {
		return nil, fmt.Errorf("no kubeconfig returned for %s", c.Name)
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig(res.Kubeconfigs[0].Value)
	if err != nil {
		return nil, err
	}

	// AAD-enabled clusters ship a kubeconfig that shells out to `kubelogin`.
	// Replace that with an in-process token so the CLI has no external deps.
	if cfg.ExecProvider != nil {
		cfg.ExecProvider = nil
		cfg.AuthProvider = nil
		cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
			return &bearerRT{base: rt, token: func(ctx context.Context) (string, error) {
				t, err := a.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{aksServerScope}})
				return t.Token, err
			}}
		}
	}
	return cfg, nil
}

type bearerRT struct {
	base  http.RoundTripper
	token func(context.Context) (string, error)
}

func (b *bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	tok, err := b.token(r.Context())
	if err != nil {
		return nil, err
	}
	r2 := r.Clone(r.Context())
	r2.Header.Set("Authorization", "Bearer "+tok)
	return b.base.RoundTrip(r2)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
