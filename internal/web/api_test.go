package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleDeployRejectsUnsupportedServiceType(t *testing.T) {
	server := &Server{}
	ctx := context.WithValue(context.Background(), userCtxKey, 1)
	request := httptest.NewRequest(http.MethodPost, "/api/deploy", strings.NewReader(
		`{"name":"shop","image":"nginx:1.27","clusters":["aws/prod"],"serviceType":"NodePort"}`,
	)).WithContext(ctx)
	response := httptest.NewRecorder()

	server.handleDeploy(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s; want %d", response.Code, response.Body, http.StatusBadRequest)
	}
	if !strings.Contains(response.Body.String(), "serviceType must be LoadBalancer or ClusterIP") {
		t.Fatalf("response does not explain supported service types: %s", response.Body)
	}
}
