package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"k8s.io/client-go/rest"
)

// AWS discovers EKS clusters using the default credential chain
// (env vars, shared config, SSO, IRSA, instance profile, ...).
type AWS struct {
	cfgs map[string]aws.Config // keyed by region
}

func NewAWS(ctx context.Context, regions []string) (*AWS, error) {
	a := &AWS{cfgs: map[string]aws.Config{}}
	for _, r := range regions {
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(r))
		if err != nil {
			return nil, err
		}
		a.cfgs[r] = cfg
	}
	return a, nil
}

func (a *AWS) Name() string { return "aws" }

func (a *AWS) ListClusters(ctx context.Context) ([]Cluster, error) {
	var out []Cluster
	var errs []error

	for region, cfg := range a.cfgs {
		client := eks.NewFromConfig(cfg)
		pager := eks.NewListClustersPaginator(client, &eks.ListClustersInput{})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", region, err))
				break
			}
			for _, name := range page.Clusters {
				d, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
				if err != nil {
					errs = append(errs, fmt.Errorf("%s/%s: %w", region, name, err))
					continue
				}
				c := d.Cluster
				ca := ""
				if c.CertificateAuthority != nil {
					ca = aws.ToString(c.CertificateAuthority.Data)
				}
				out = append(out, Cluster{
					Provider: "aws",
					Name:     aws.ToString(c.Name),
					Region:   region,
					Version:  aws.ToString(c.Version),
					Status:   string(c.Status),
					Meta: map[string]string{
						"endpoint": aws.ToString(c.Endpoint),
						"ca":       ca,
					},
				})
			}
		}
	}
	return out, errors.Join(errs...)
}

func (a *AWS) RESTConfig(ctx context.Context, c Cluster) (*rest.Config, error) {
	ca, err := base64.StdEncoding.DecodeString(c.Meta["ca"])
	if err != nil {
		return nil, fmt.Errorf("decode CA: %w", err)
	}
	tok, err := eksToken(ctx, a.cfgs[c.Region], c.Name)
	if err != nil {
		return nil, fmt.Errorf("eks token: %w", err)
	}
	return &rest.Config{
		Host:            c.Meta["endpoint"],
		BearerToken:     tok,
		TLSClientConfig: rest.TLSClientConfig{CAData: ca},
	}, nil
}

// eksToken builds the same bearer token `aws eks get-token` produces:
// a presigned STS GetCallerIdentity URL bound to the cluster name.
func eksToken(ctx context.Context, cfg aws.Config, cluster string) (string, error) {
	presigner := sts.NewPresignClient(sts.NewFromConfig(cfg))
	req, err := presigner.PresignGetCallerIdentity(ctx, &sts.GetCallerIdentityInput{},
		func(po *sts.PresignOptions) {
			po.ClientOptions = append(po.ClientOptions, func(o *sts.Options) {
				o.APIOptions = append(o.APIOptions, eksParams(cluster))
			})
		})
	if err != nil {
		return "", err
	}
	return "k8s-aws-v1." + base64.RawURLEncoding.EncodeToString([]byte(req.URL)), nil
}

// eksParams adds the cluster-id header (signed) and a short expiry.
func eksParams(cluster string) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Build.Add(middleware.BuildMiddlewareFunc("EKSTokenParams",
			func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (
				middleware.BuildOutput, middleware.Metadata, error) {
				if r, ok := in.Request.(*smithyhttp.Request); ok {
					q := r.URL.Query()
					q.Set("X-Amz-Expires", "60")
					r.URL.RawQuery = q.Encode()
					r.Header.Set("x-k8s-aws-id", cluster)
				}
				return next.HandleBuild(ctx, in)
			}), middleware.After)
	}
}
