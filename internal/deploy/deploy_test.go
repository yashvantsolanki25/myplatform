package deploy

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBuildServiceUsesRequestedExposure(t *testing.T) {
	tests := []struct {
		name        string
		serviceType corev1.ServiceType
		want        corev1.ServiceType
	}{
		{name: "default is public", want: corev1.ServiceTypeLoadBalancer},
		{name: "internal only", serviceType: corev1.ServiceTypeClusterIP, want: corev1.ServiceTypeClusterIP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildService(Spec{Name: "shop", Namespace: "default", Port: 8080, ServiceType: tt.serviceType})
			if got.Spec.Type != tt.want {
				t.Fatalf("service type = %q; want %q", got.Spec.Type, tt.want)
			}
		})
	}
}

func TestGetEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		serviceType corev1.ServiceType
		ingress     []corev1.LoadBalancerIngress
		want        string
	}{
		{
			name:        "hostname",
			serviceType: corev1.ServiceTypeLoadBalancer,
			ingress:     []corev1.LoadBalancerIngress{{Hostname: "shop.example.test"}},
			want:        "http://shop.example.test",
		},
		{
			name:        "ipv4",
			serviceType: corev1.ServiceTypeLoadBalancer,
			ingress:     []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}},
			want:        "http://203.0.113.10",
		},
		{
			name:        "ipv6",
			serviceType: corev1.ServiceTypeLoadBalancer,
			ingress:     []corev1.LoadBalancerIngress{{IP: "2001:db8::1"}},
			want:        "http://[2001:db8::1]",
		},
		{
			name:        "internal service has no public URL",
			serviceType: corev1.ServiceTypeClusterIP,
			ingress:     []corev1.LoadBalancerIngress{{IP: "203.0.113.10"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(&corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "default"},
				Spec:       corev1.ServiceSpec{Type: tt.serviceType},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{Ingress: tt.ingress},
				},
			})
			got, err := GetEndpoint(context.Background(), client, "default", "shop")
			if err != nil {
				t.Fatalf("GetEndpoint returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("endpoint = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestApplyUpdatesServiceExposureAndKeepsClusterIP(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				Type:      corev1.ServiceTypeClusterIP,
				ClusterIP: "10.0.0.5",
			},
		},
	)
	spec := Spec{
		Name:        "shop",
		Namespace:   "default",
		Image:       "nginx:1.27",
		Port:        8080,
		Replicas:    1,
		ServiceType: corev1.ServiceTypeLoadBalancer,
	}

	if err := Apply(context.Background(), client, spec); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	got, err := client.CoreV1().Services("default").Get(context.Background(), "shop", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading service: %v", err)
	}
	if got.Spec.Type != corev1.ServiceTypeLoadBalancer {
		t.Errorf("service type = %q; want %q", got.Spec.Type, corev1.ServiceTypeLoadBalancer)
	}
	if got.Spec.ClusterIP != "10.0.0.5" {
		t.Errorf("cluster IP = %q; want existing allocation to be preserved", got.Spec.ClusterIP)
	}
}
