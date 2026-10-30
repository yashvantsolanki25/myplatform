package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/example/myplatform/internal/deploy"
	"github.com/example/myplatform/internal/provider"
	"golang.org/x/oauth2/google"
	"k8s.io/client-go/kubernetes"
)

// target pairs a cluster with the provider that can authenticate to it.
type target struct {
	p provider.Provider
	c provider.Cluster
}

// buildUserOptions reads the user's stored (encrypted) credentials, decrypts
// them, and builds provider.Options with explicit credential objects.
func (s *Server) buildUserOptions(userID int) (provider.Options, error) {
	var opts provider.Options

	has, err := s.store.HasCredentials(userID, "aws")
	if err != nil {
		return opts, err
	}
	if has {
		var cfg AWSCredConfig
		if err := s.store.GetDecryptedCredentials(userID, "aws", &cfg); err != nil {
			return opts, err
		}
		opts.AWSRegions = cfg.Regions
		if cfg.AccessKeyID != "" {
			opts.AWSCreds = &provider.AWSCredentials{
				AccessKeyID:     cfg.AccessKeyID,
				SecretAccessKey: cfg.SecretAccessKey,
				SessionToken:    cfg.SessionToken,
			}
		}
	}

	has, err = s.store.HasCredentials(userID, "gcp")
	if err != nil {
		return opts, err
	}
	if has {
		var cfg GCPCredConfig
		if err := s.store.GetDecryptedCredentials(userID, "gcp", &cfg); err != nil {
			return opts, err
		}
		opts.GCPProject = cfg.ProjectID
		if cfg.ServiceAccountKey != "" {
			opts.GCPCreds = &provider.GCPCredentials{
				ServiceAccountJSON: cfg.ServiceAccountKey,
			}
		}
	}

	has, err = s.store.HasCredentials(userID, "azure")
	if err != nil {
		return opts, err
	}
	if has {
		var cfg AzureCredConfig
		if err := s.store.GetDecryptedCredentials(userID, "azure", &cfg); err != nil {
			return opts, err
		}
		opts.AzureSubscription = cfg.SubscriptionID
		if cfg.ClientID != "" {
			opts.AzureCreds = &provider.AzureCredentials{
				ClientID:     cfg.ClientID,
				ClientSecret: cfg.ClientSecret,
				TenantID:     cfg.TenantID,
			}
		}
	}

	return opts, nil
}

// discover lists clusters from every configured provider for the given user.
func (s *Server) discover(ctx context.Context, userID int) ([]target, []string, error) {
	opts, err := s.buildUserOptions(userID)
	if err != nil {
		return nil, nil, err
	}
	if len(opts.AWSRegions) == 0 && opts.GCPProject == "" && opts.AzureSubscription == "" {
		return nil, nil, fmt.Errorf("no cloud credentials configured; open Cloud Credentials settings")
	}
	provs, err := provider.Build(ctx, opts)
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("database unavailable"))
		return
	}
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
	userID, err := userIDFromCtx(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	ctx := r.Context()
	all, warnings, err := s.discover(ctx, userID)
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
	UserID    int         `json:"-"`
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
	userID, err := userIDFromCtx(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
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
	all, _, err := s.discover(ctx, userID)
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
		UserID: userID,
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
	userID, err := userIDFromCtx(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	id := r.PathValue("id")
	jobsMu.RLock()
	j, ok := jobs[id]
	jobsMu.RUnlock()
	if !ok || j.UserID != userID {
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
	userID, err := userIDFromCtx(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
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
	all, _, err := s.discover(ctx, userID)
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

// --- credential handlers ---

func (s *Server) handleGetCredentials(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromCtx(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	result := map[string]any{}

	// Return only non-secret fields; credential values are never sent back.
	has, err := s.store.HasCredentials(userID, "aws")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("load credentials"))
		return
	}
	if has {
		var cfg AWSCredConfig
		if err := s.store.GetDecryptedCredentials(userID, "aws", &cfg); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("load credentials"))
			return
		}
		result["aws"] = map[string]any{
			"configured":  true,
			"regions":     cfg.Regions,
			"accessKeyId": maskSecret(cfg.AccessKeyID),
		}
	}
	if result["aws"] == nil {
		result["aws"] = map[string]any{"configured": false}
	}

	// GCP
	has, err = s.store.HasCredentials(userID, "gcp")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("load credentials"))
		return
	}
	if has {
		var cfg GCPCredConfig
		if err := s.store.GetDecryptedCredentials(userID, "gcp", &cfg); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("load credentials"))
			return
		}
		result["gcp"] = map[string]any{
			"configured": true,
			"projectId":  cfg.ProjectID,
		}
	}
	if result["gcp"] == nil {
		result["gcp"] = map[string]any{"configured": false}
	}

	// Azure
	has, err = s.store.HasCredentials(userID, "azure")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("load credentials"))
		return
	}
	if has {
		var cfg AzureCredConfig
		if err := s.store.GetDecryptedCredentials(userID, "azure", &cfg); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("load credentials"))
			return
		}
		result["azure"] = map[string]any{
			"configured":     true,
			"subscriptionId": cfg.SubscriptionID,
			"tenantId":       cfg.TenantID,
		}
	}
	if result["azure"] == nil {
		result["azure"] = map[string]any{"configured": false}
	}

	writeJSON(w, http.StatusOK, result)
}

// maskSecret shows the last 4 chars of a secret for verification without
// exposing the full value (A02).
func maskSecret(s string) string {
	if len(s) <= 4 {
		return "••••"
	}
	return "••••••" + s[len(s)-4:]
}

func (s *Server) handleSaveCredentials(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromCtx(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	prov := r.PathValue("provider")
	if prov != "aws" && prov != "gcp" && prov != "azure" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid provider: %s", prov))
		return
	}

	var raw json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	switch prov {
	case "aws":
		var cfg AWSCredConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		cfg.AccessKeyID = strings.TrimSpace(cfg.AccessKeyID)
		cfg.SecretAccessKey = strings.TrimSpace(cfg.SecretAccessKey)
		cfg.SessionToken = strings.TrimSpace(cfg.SessionToken)
		for i := range cfg.Regions {
			cfg.Regions[i] = strings.TrimSpace(cfg.Regions[i])
		}
		cfg.Regions = compactStrings(cfg.Regions)
		if len(cfg.Regions) == 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("at least one AWS region is required"))
			return
		}
		hasAccessKey := cfg.AccessKeyID != ""
		hasSecretKey := cfg.SecretAccessKey != ""
		if hasAccessKey != hasSecretKey {
			writeError(w, http.StatusBadRequest, fmt.Errorf("accessKeyId and secretAccessKey must both be provided"))
			return
		}
		if !hasAccessKey && cfg.SessionToken != "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("sessionToken requires AWS access keys"))
			return
		}
		if err := s.store.SaveCredentials(userID, "aws", cfg); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	case "gcp":
		var cfg GCPCredConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		cfg.ServiceAccountKey = strings.TrimSpace(cfg.ServiceAccountKey)
		cfg.ProjectID = strings.TrimSpace(cfg.ProjectID)
		if cfg.ProjectID == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("projectId is required"))
			return
		}
		if cfg.ServiceAccountKey != "" {
			if _, err := google.JWTConfigFromJSON(
				[]byte(cfg.ServiceAccountKey),
				"https://www.googleapis.com/auth/cloud-platform",
			); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("invalid Google service account JSON"))
				return
			}
		}
		if err := s.store.SaveCredentials(userID, "gcp", cfg); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	case "azure":
		var cfg AzureCredConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		cfg.ClientID = strings.TrimSpace(cfg.ClientID)
		cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
		cfg.TenantID = strings.TrimSpace(cfg.TenantID)
		cfg.SubscriptionID = strings.TrimSpace(cfg.SubscriptionID)
		if cfg.SubscriptionID == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("subscriptionId is required"))
			return
		}
		hasClientID := cfg.ClientID != ""
		hasClientSecret := cfg.ClientSecret != ""
		hasTenantID := cfg.TenantID != ""
		if hasClientID != hasClientSecret || hasClientID != hasTenantID {
			writeError(w, http.StatusBadRequest, fmt.Errorf("clientId, clientSecret, and tenantId must all be provided together"))
			return
		}
		if err := s.store.SaveCredentials(userID, "azure", cfg); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	fmt.Printf("[cred] saved %s credentials for user %d\n", prov, userID) // A09: logging
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func compactStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func (s *Server) handleDeleteCredentials(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromCtx(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	prov := r.PathValue("provider")
	if prov != "aws" && prov != "gcp" && prov != "azure" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid provider: %s", prov))
		return
	}
	if err := s.store.DeleteCredentials(userID, prov); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	fmt.Printf("[cred] deleted %s credentials for user %d\n", prov, userID) // A09
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
