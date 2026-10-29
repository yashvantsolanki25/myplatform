// Package deploy applies an app to a cluster through client-go. It knows
// nothing about clouds: it only needs a kubernetes.Interface.
package deploy

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

type Spec struct {
	Name      string
	Namespace string
	Image     string
	Port      int32
	Replicas  int32
	Env       map[string]string
}

type Status struct {
	Desired int32
	Ready   int32
	Updated int32
}

func (s Status) String() string { return fmt.Sprintf("%d/%d", s.Ready, s.Desired) }

func labels(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "myplatform",
	}
}

// Apply creates or updates the namespace, Deployment and Service.
func Apply(ctx context.Context, cs kubernetes.Interface, s Spec) error {
	if _, err := cs.CoreV1().Namespaces().Get(ctx, s.Namespace, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: s.Namespace}}
		if _, err := cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("namespace: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("namespace: %w", err)
	}

	deps := cs.AppsV1().Deployments(s.Namespace)
	want := buildDeployment(s)
	cur, err := deps.Get(ctx, s.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = deps.Create(ctx, want, metav1.CreateOptions{})
	case err == nil:
		want.ResourceVersion = cur.ResourceVersion
		_, err = deps.Update(ctx, want, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("deployment: %w", err)
	}

	svcs := cs.CoreV1().Services(s.Namespace)
	wantSvc := buildService(s)
	curSvc, err := svcs.Get(ctx, s.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = svcs.Create(ctx, wantSvc, metav1.CreateOptions{})
	case err == nil:
		// Keep the allocated ClusterIP; only reconcile ports and selector.
		curSvc.Spec.Ports = wantSvc.Spec.Ports
		curSvc.Spec.Selector = wantSvc.Spec.Selector
		_, err = svcs.Update(ctx, curSvc, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("service: %w", err)
	}
	return nil
}

func buildDeployment(s Spec) *appsv1.Deployment {
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := make([]corev1.EnvVar, 0, len(keys))
	for _, k := range keys {
		env = append(env, corev1.EnvVar{Name: k, Value: s.Env[k]})
	}

	replicas := s.Replicas
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: s.Namespace, Labels: labels(s.Name)},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": s.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels(s.Name)},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:  s.Name,
					Image: s.Image,
					Ports: []corev1.ContainerPort{{ContainerPort: s.Port}},
					Env:   env,
				}}},
			},
		},
	}
}

func buildService(s Spec) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: s.Name, Namespace: s.Namespace, Labels: labels(s.Name)},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app.kubernetes.io/name": s.Name},
			Ports: []corev1.ServicePort{{
				Port:       80,
				TargetPort: intstr.FromInt32(s.Port),
			}},
		},
	}
}

// GetStatus reports ready/desired replicas for a Deployment.
func GetStatus(ctx context.Context, cs kubernetes.Interface, ns, name string) (Status, error) {
	d, err := cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return Status{}, err
	}
	st := Status{Ready: d.Status.ReadyReplicas, Updated: d.Status.UpdatedReplicas}
	if d.Spec.Replicas != nil {
		st.Desired = *d.Spec.Replicas
	}
	return st, nil
}

// WaitReady blocks until every replica is updated and ready, or times out.
func WaitReady(ctx context.Context, cs kubernetes.Interface, ns, name string, timeout time.Duration) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var last Status
	var lastErr error
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		d, err := cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			lastErr = nil
			last = Status{Ready: d.Status.ReadyReplicas, Updated: d.Status.UpdatedReplicas}
			if d.Spec.Replicas != nil {
				last.Desired = *d.Spec.Replicas
			}
			if d.Status.ObservedGeneration >= d.Generation &&
				last.Ready == last.Desired && last.Updated == last.Desired {
				return last, nil
			}
		} else if !apierrors.IsNotFound(err) {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return last, fmt.Errorf("timed out waiting for rollout (%s ready): %w", last, lastErr)
			}
			return last, fmt.Errorf("timed out waiting for rollout (%s ready)", last)
		case <-tick.C:
		}
	}
}
