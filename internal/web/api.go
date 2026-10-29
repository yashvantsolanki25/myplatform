package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/example/myplatform/internal/deploy"
	"github.com/example/myplatform/internal/provider"
	"k8s.io/client-go/kubernetes"
)

// target pairs a cluster with the provider that can authenticate to it.
type target struct {
	p provider.Provider
	c provider.Cluster
}

// discover lists clusters from every configured provider, keeping
// partial results even when a provider returns an error.
func (s *Server) discover(ctx context.Context) ([]target, []string, error) {
	provs, err := provider.Build(ctx, s.getOpts())
	if err != nil {
		return nil, nil, err
	}
	var out []target
	var warnings []string
	for _, p := range provs {
		cs, err := p.ListClusters(ctx)
		if err != nil && len(cs) == 0 {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p.Name(), err))
			continue
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p.Name(), err))
		}
		for _, c := range cs {
			out = append(out, target{p: p, c: c})
		}
	}
	return out, warnings, nil
}

func resolveTargets(all []target, refs []string) ([]target, error) {
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
			return nil, fmt.Errorf("cluster %q not found (refresh the clusters list)", ref)
		case 1:
			out = append(out, matches[0])
		default:
			return nil, fmt.Errorf("cluster %q is ambiguous; use provider/name", ref)
		}
	}
	return out, nil
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// --- handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type clusterOut struct {
	Ref      string `json:"ref"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Region   string `json:"region"`
	Version  string `json:"version"`
	Status   string `json:"status"`
}

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	all, warnings, err := s.discover(ctx)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	clusters := make([]clusterOut, 0, len(all))
	for _, t := range all {
		clusters = append(clusters, clusterOut{
			Ref:      t.c.Ref(),
			Provider: t.c.Provider,
			Name:     t.c.Name,
			Region:   t.c.Region,
			Version:  t.c.Version,
			Status:   t.c.Status,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"clusters": clusters,
		"warnings": warnings,
	})
}

// --- deploy job management ---

type jobResult struct {
	Ref   string `json:"ref"`
	OK    bool   `json:"ok"`
	Ready string `json:"ready"`
	Error string `json:"error,omitempty"`
}

type job struct {
	ID        string      `json:"id"`
	Status    string      `json:"status"`
	Name      string      `json:"name"`
	Image     string      `json:"image"`
	Total     int         `json:"total"`
	Completed int         `json:"completed"`
	Failed    int         `json:"failed"`
	Results   []jobResult `json:"results"`
	mu        sync.Mutex
}

var (
	jobs   = map[string]*job{}
	jobsMu sync.RWMutex
)

func newJobID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (j *job) addResult(r jobResult) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Results = append(j.Results, r)
	j.Completed++
	if !r.OK {
		j.Failed++
	}
}

func (j *job) snapshot() job {
	j.mu.Lock()
	defer j.mu.Unlock()
	return job{
		ID:        j.ID,
		Status:    j.Status,
		Name:      j.Name,
		Image:     j.Image,
		Total:     j.Total,
		Completed: j.Completed,
		Failed:    j.Failed,
		Results:   append([]jobResult(nil), j.Results...),
	}
}

type deployRequest struct {
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	Namespace string            `json:"namespace"`
	Port      int32             `json:"port"`
	Replicas  int32             `json:"replicas"`
	Env       map[string]string `json:"env"`
	Clusters  []string          `json:"clusters"`
	Wait      string            `json:"wait"`
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	var req deployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body"))
		return
	}
	if req.Name == "" || req.Image == "" || len(req.Clusters) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name, image, and clusters are required"))
		return
	}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if req.Port == 0 {
		req.Port = 8080
	}
	if req.Replicas == 0 {
		req.Replicas = 2
	}
	wait, err := time.ParseDuration(req.Wait)
	if err != nil || wait == 0 {
		wait = 2 * time.Minute
	}

	ctx := r.Context()
	all, _, err := s.discover(ctx)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	targets, err := resolveTargets(all, req.Clusters)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	j := &job{
		ID:     newJobID(),
		Status: "running",
		Name:   req.Name,
		Image:  req.Image,
		Total:  len(targets),
	}
	jobsMu.Lock()
	jobs[j.ID] = j
	jobsMu.Unlock()

	spec := deploy.Spec{
		Name:      req.Name,
		Namespace: req.Namespace,
		Image:     req.Image,
		Port:      req.Port,
		Replicas:  req.Replicas,
		Env:       req.Env,
	}

	// Run deployments in the background using the server context so they
	// survive after the HTTP response is sent.
	go s.runJob(s.ctx, j, targets, spec, wait)

	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": j.ID})
}

func (s *Server) runJob(ctx context.Context, j *job, targets []target, spec deploy.Spec, wait time.Duration) {
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			r := jobResult{Ref: t.c.Ref()}

			cfg, err := t.p.RESTConfig(ctx, t.c)
			if err != nil {
				r.Error = "auth: " + err.Error()
				j.addResult(r)
				return
			}
			cs, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				r.Error = err.Error()
				j.addResult(r)
				return
			}
			if err := deploy.Apply(ctx, cs, spec); err != nil {
				r.Error = err.Error()
				j.addResult(r)
				return
			}
			var st deploy.Status
			if wait > 0 {
				st, err = deploy.WaitReady(ctx, cs, spec.Namespace, spec.Name, wait)
			} else {
				st, err = deploy.GetStatus(ctx, cs, spec.Namespace, spec.Name)
			}
			if err != nil {
				r.Error = err.Error()
			} else {
				r.OK = true
				r.Ready = st.String()
			}
			j.addResult(r)
		}(t)
	}
	wg.Wait()

	j.mu.Lock()
	j.Status = "completed"
	j.mu.Unlock()
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	jobsMu.RLock()
	j, ok := jobs[id]
	jobsMu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("job not found"))
		return
	}
	writeJSON(w, http.StatusOK, j.snapshot())
}

type statusOut struct {
	Ref     string `json:"ref"`
	Ready   string `json:"ready"`
	Updated int32  `json:"updated"`
	Desired int32  `json:"desired"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = "default"
	}
	clusterRefs := r.URL.Query()["cluster"]

	ctx := r.Context()
	all, _, err := s.discover(ctx)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	targets := all
	if len(clusterRefs) > 0 {
		targets, err = resolveTargets(all, clusterRefs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	out := make([]statusOut, 0, len(targets))
	for _, t := range targets {
		so := statusOut{Ref: t.c.Ref()}
		cfg, err := t.p.RESTConfig(ctx, t.c)
		if err != nil {
			so.Error = "auth: " + err.Error()
			out = append(out, so)
			continue
		}
		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			so.Error = err.Error()
			out = append(out, so)
			continue
		}
		st, err := deploy.GetStatus(ctx, cs, ns, name)
		if err != nil {
			so.Error = err.Error()
			out = append(out, so)
			continue
		}
		so.Ready = st.String()
		so.Updated = st.Updated
		so.Desired = st.Desired
		out = append(out, so)
	}
	writeJSON(w, http.StatusOK, map[string]any{"statuses": out})
}

// --- config handlers ---

type configOut struct {
	AWSRegions        []string `json:"awsRegions"`
	GCPProject        string   `json:"gcpProject"`
	AzureSubscription string   `json:"azureSubscription"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	opts := s.getOpts()
	regions := opts.AWSRegions
	if regions == nil {
		regions = []string{}
	}
	writeJSON(w, http.StatusOK, configOut{
		AWSRegions:        regions,
		GCPProject:        opts.GCPProject,
		AzureSubscription: opts.AzureSubscription,
	})
}

type configRequest struct {
	AWSRegions        []string `json:"awsRegions"`
	GCPProject        string   `json:"gcpProject"`
	AzureSubscription string   `json:"azureSubscription"`
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var req configRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body"))
		return
	}
	s.setOpts(provider.Options{
		AWSRegions:        req.AWSRegions,
		GCPProject:        req.GCPProject,
		AzureSubscription: req.AzureSubscription,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
