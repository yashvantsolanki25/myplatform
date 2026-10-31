package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("MYPLATFORM_DATA_DIR", t.TempDir())
	t.Setenv("MYPLATFORM_ENCRYPTION_KEY", "")
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func TestStorePersistsUsersAndEncryptedCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MYPLATFORM_DATA_DIR", dir)
	t.Setenv("MYPLATFORM_ENCRYPTION_KEY", "")
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}

	user, err := store.CreateUser(" User@example.com ", "Test User", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "user@example.com" {
		t.Fatalf("email = %q; want normalized email", user.Email)
	}
	if _, err := store.CreateUser("user@example.com", "Another User", "password-123"); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("duplicate registration error = %v; want ErrEmailExists", err)
	}

	want := AWSCredConfig{
		AccessKeyID:     "AKIATEST",
		SecretAccessKey: "secret-value",
		Regions:         []string{"us-east-1"},
	}
	if err := store.SaveCredentials(user.ID, "aws", want); err != nil {
		t.Fatal(err)
	}
	has, err := store.HasCredentials(user.ID, "aws")
	if err != nil || !has {
		t.Fatalf("HasCredentials = %t, %v; want true, nil", has, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	gotUser, err := reopened.GetUserByEmail("USER@example.com")
	if err != nil || gotUser.ID != user.ID {
		t.Fatalf("GetUserByEmail = %#v, %v; want stored user", gotUser, err)
	}
	var got AWSCredConfig
	if err := reopened.GetDecryptedCredentials(user.ID, "aws", &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != want.AccessKeyID || got.SecretAccessKey != want.SecretAccessKey || len(got.Regions) != 1 || got.Regions[0] != want.Regions[0] {
		t.Fatalf("decrypted credentials = %#v; want %#v", got, want)
	}
	if err := reopened.DeleteCredentials(user.ID, "aws"); err != nil {
		t.Fatal(err)
	}
	has, err = reopened.HasCredentials(user.ID, "aws")
	if err != nil || has {
		t.Fatalf("HasCredentials after deletion = %t, %v; want false, nil", has, err)
	}
}

func TestStoreRejectsInvalidConfiguredEncryptionKey(t *testing.T) {
	t.Setenv("MYPLATFORM_DATA_DIR", t.TempDir())
	t.Setenv("MYPLATFORM_ENCRYPTION_KEY", "not-a-key")
	if _, err := NewStore(); err == nil {
		t.Fatal("NewStore accepted an invalid encryption key")
	}
}

func TestRegisterAndLogin(t *testing.T) {
	store := testStore(t)
	server := &Server{store: store, rl: newRateLimiter()}

	register := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewBufferString(
		`{"name":" Test User ","email":" User@Example.com ","password":"password-123"}`,
	))
	register.RemoteAddr = "127.0.0.1:1234"
	registerResponse := httptest.NewRecorder()
	server.handleRegister(registerResponse, register)
	if registerResponse.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerResponse.Code, registerResponse.Body)
	}

	var registered struct {
		Token string `json:"token"`
		User  struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"user"`
	}
	if err := json.Unmarshal(registerResponse.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	if registered.Token == "" || registered.User.Email != "user@example.com" || registered.User.Name != "Test User" {
		t.Fatalf("unexpected registration response: %#v", registered)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(
		`{"email":"USER@example.com","password":"password-123"}`,
	))
	login.RemoteAddr = "127.0.0.1:1234"
	loginResponse := httptest.NewRecorder()
	server.handleLogin(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginResponse.Code, loginResponse.Body)
	}

	badPassword := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(
		`{"email":"user@example.com","password":"wrong-password"}`,
	))
	badPassword.RemoteAddr = "127.0.0.1:1234"
	badPasswordResponse := httptest.NewRecorder()
	server.handleLogin(badPasswordResponse, badPassword)
	if badPasswordResponse.Code != http.StatusUnauthorized {
		t.Fatalf("bad password status = %d; want %d", badPasswordResponse.Code, http.StatusUnauthorized)
	}

	duplicate := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewBufferString(
		`{"name":"Test User","email":"user@example.com","password":"password-123"}`,
	))
	duplicate.RemoteAddr = "127.0.0.1:5678"
	duplicateResponse := httptest.NewRecorder()
	server.handleRegister(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusConflict {
		t.Fatalf("duplicate registration status = %d; want %d", duplicateResponse.Code, http.StatusConflict)
	}
}

func TestNewStoreHonorsDataDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MYPLATFORM_DATA_DIR", dir)
	t.Setenv("MYPLATFORM_ENCRYPTION_KEY", "")
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "/secret.key"); err != nil {
		t.Fatalf("encryption key was not persisted in configured data directory: %v", err)
	}
}

func TestCloudCredentialSubmissionAndSecretRedaction(t *testing.T) {
	store := testStore(t)
	user, err := store.CreateUser("clouds@example.com", "Cloud User", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		store: store,
		credentialValidator: func(context.Context, string, any) error {
			return nil
		},
	}
	ctx := context.WithValue(context.Background(), userCtxKey, user.ID)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	serviceAccountKey, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyBytes})),
		"client_email": "smoke@example.invalid",
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	gcpBody, err := json.Marshal(GCPCredConfig{
		ServiceAccountKey: string(serviceAccountKey),
		ProjectID:         "example-project",
	})
	if err != nil {
		t.Fatal(err)
	}
	credentials := map[string]string{
		"aws":   `{"accessKeyId":"AKIATEST","secretAccessKey":"aws-secret","regions":["us-east-1"]}`,
		"gcp":   string(gcpBody),
		"azure": `{"clientId":"client-id","clientSecret":"azure-secret","tenantId":"tenant-id","subscriptionId":"subscription-id"}`,
	}
	for provider, body := range credentials {
		request := httptest.NewRequest(http.MethodPut, "/api/credentials/"+provider, strings.NewReader(body))
		request = request.WithContext(ctx)
		request.SetPathValue("provider", provider)
		response := httptest.NewRecorder()
		server.handleSaveCredentials(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("%s save status = %d, body = %s", provider, response.Code, response.Body)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/credentials", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	server.handleGetCredentials(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("get credentials status = %d, body = %s", response.Code, response.Body)
	}
	for _, secret := range []string{"aws-secret", string(serviceAccountKey), "azure-secret", "private_key"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Errorf("credential response exposed secret marker %q", secret)
		}
	}
	for _, provider := range []string{`"aws"`, `"gcp"`, `"azure"`} {
		if !strings.Contains(response.Body.String(), provider) {
			t.Errorf("credential response missing provider %s", provider)
		}
	}
}

func TestCredentialValidationFailureDoesNotSaveCredentials(t *testing.T) {
	store := testStore(t)
	user, err := store.CreateUser("validation@example.com", "Validation User", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	old := AWSCredConfig{
		AccessKeyID:     "AKIAOLD",
		SecretAccessKey: "old-secret",
		Regions:         []string{"us-east-1"},
	}
	if err := store.SaveCredentials(user.ID, "aws", old); err != nil {
		t.Fatal(err)
	}

	server := &Server{
		store: store,
		credentialValidator: func(context.Context, string, any) error {
			return errors.New("access denied")
		},
	}
	request := httptest.NewRequest(http.MethodPut, "/api/credentials/aws", strings.NewReader(
		`{"accessKeyId":"AKIANEW","secretAccessKey":"new-secret","regions":["us-west-2"]}`,
	)).WithContext(context.WithValue(context.Background(), userCtxKey, user.ID))
	request.SetPathValue("provider", "aws")
	response := httptest.NewRecorder()
	server.handleSaveCredentials(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("save status = %d, body = %s; want %d", response.Code, response.Body, http.StatusUnprocessableEntity)
	}

	var got AWSCredConfig
	if err := store.GetDecryptedCredentials(user.ID, "aws", &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessKeyID != old.AccessKeyID || got.SecretAccessKey != old.SecretAccessKey || got.Regions[0] != old.Regions[0] {
		t.Fatalf("stored credentials after failed validation = %#v; want %#v", got, old)
	}
}

func TestJobResultsAreScopedToTheOwner(t *testing.T) {
	jobsMu.Lock()
	jobs = map[string]*job{}
	jobsMu.Unlock()
	owner := &job{ID: newJobID(), UserID: 1, Status: "completed"}
	jobsMu.Lock()
	jobs[owner.ID] = owner
	jobsMu.Unlock()

	request := httptest.NewRequest(http.MethodGet, "/api/jobs/"+owner.ID, nil)
	request.SetPathValue("id", owner.ID)
	otherUserRequest := request.WithContext(context.WithValue(request.Context(), userCtxKey, 2))
	otherUserResponse := httptest.NewRecorder()
	(&Server{}).handleJob(otherUserResponse, otherUserRequest)
	if otherUserResponse.Code != http.StatusNotFound {
		t.Fatalf("other user's job status = %d; want %d", otherUserResponse.Code, http.StatusNotFound)
	}

	ownerRequest := request.WithContext(context.WithValue(request.Context(), userCtxKey, 1))
	ownerResponse := httptest.NewRecorder()
	(&Server{}).handleJob(ownerResponse, ownerRequest)
	if ownerResponse.Code != http.StatusOK {
		t.Fatalf("owner's job status = %d; want %d", ownerResponse.Code, http.StatusOK)
	}
}
