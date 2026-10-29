package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"

	"github.com/example/myplatform/internal/deploy"
)

var deployFlags struct {
	name, namespace, image string
	port, replicas         int32
	env                    []string
	clusters               []string
	wait                   time.Duration
}

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy an app to one or more clusters in parallel",
	Example: `  myplatform deploy --name shop --image nginx:1.27 --port 80 \
    --cluster aws/prod-eks --cluster gcp/prod-gke --cluster azure/prod-aks`,
	RunE: runDeploy,
}

func init() {
	f := deployCmd.Flags()
	f.StringVar(&deployFlags.name, "name", "", "app name (required)")
	f.StringVar(&deployFlags.image, "image", "", "container image (required)")
	f.StringVarP(&deployFlags.namespace, "namespace", "n", "default", "target namespace")
	f.Int32Var(&deployFlags.port, "port", 8080, "container port")
	f.Int32Var(&deployFlags.replicas, "replicas", 2, "replicas per cluster")
	f.StringArrayVarP(&deployFlags.env, "env", "e", nil, "environment variable KEY=VALUE (repeatable)")
	f.StringSliceVar(&deployFlags.clusters, "cluster", nil, "target cluster, provider/name (repeatable, required)")
	f.DurationVar(&deployFlags.wait, "wait", 2*time.Minute, "max time to wait for rollout (0 = don't wait)")
	_ = deployCmd.MarkFlagRequired("name")
	_ = deployCmd.MarkFlagRequired("image")
	_ = deployCmd.MarkFlagRequired("cluster")
}

type result struct {
	ref    string
	status deploy.Status
	err    error
}

func runDeploy(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	envMap, err := parseEnv(deployFlags.env)
	if err != nil {
		return err
	}
	provs, err := buildProviders(ctx)
	if err != nil {
		return err
	}
	targets, err := resolve(discover(ctx, provs), deployFlags.clusters)
	if err != nil {
		return err
	}

	spec := deploy.Spec{
		Name:      deployFlags.name,
		Namespace: deployFlags.namespace,
		Image:     deployFlags.image,
		Port:      deployFlags.port,
		Replicas:  deployFlags.replicas,
		Env:       envMap,
	}

	// One goroutine per cluster. A failure in one never aborts the others,
	// so partial failure is reported explicitly instead of hidden.
	results := make([]result, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			results[i] = deployOne(ctx, t, spec, deployFlags.wait)
		}(i, t)
	}
	wg.Wait()

	return report(results)
}

func deployOne(ctx context.Context, t target, spec deploy.Spec, wait time.Duration) result {
	r := result{ref: t.c.Ref()}

	cfg, err := t.p.RESTConfig(ctx, t.c)
	if err != nil {
		r.err = fmt.Errorf("auth: %w", err)
		return r
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		r.err = err
		return r
	}
	if err := deploy.Apply(ctx, cs, spec); err != nil {
		r.err = err
		return r
	}
	if wait > 0 {
		r.status, r.err = deploy.WaitReady(ctx, cs, spec.Namespace, spec.Name, wait)
	} else {
		r.status, r.err = deploy.GetStatus(ctx, cs, spec.Namespace, spec.Name)
	}
	return r
}

func report(results []result) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "CLUSTER\tRESULT\tREADY\tDETAIL")
	var failed []string
	for _, r := range results {
		if r.err != nil {
			failed = append(failed, r.ref)
			fmt.Fprintf(w, "%s\tFAILED\t%s\t%v\n", r.ref, r.status, r.err)
			continue
		}
		fmt.Fprintf(w, "%s\tOK\t%s\t\n", r.ref, r.status)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d clusters failed: %s", len(failed), len(results), strings.Join(failed, ", "))
	}
	return nil
}

func parseEnv(pairs []string) (map[string]string, error) {
	m := map[string]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, errors.New("invalid --env " + p + " (want KEY=VALUE)")
		}
		m[k] = v
	}
	return m, nil
}
