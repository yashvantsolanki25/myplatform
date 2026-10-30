package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

// --- input validation (A03: injection prevention) ---

func validateEmail(email string) error {
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || len(email) < 5 || len(email) > 254 {
		return fmt.Errorf("invalid email address")
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	if len(password) > 72 {
		return fmt.Errorf("password must be no more than 72 bytes")
	}
	return nil
}

func validateName(name string) error {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 80 {
		return fmt.Errorf("name must be 1-80 characters")
	}
	return nil
}

// --- auth handlers ---

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.rl.Allow("register:"+ip, 5, time.Minute) { // A07: rate limit
		writeError(w, http.StatusTooManyRequests, fmt.Errorf("too many registration attempts, try again later"))
		return
	}

	var req registerRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Name = strings.TrimSpace(req.Name)

	if err := validateEmail(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	user, err := s.store.CreateUser(req.Email, req.Name, req.Password)
	if err != nil {
		if errors.Is(err, ErrEmailExists) {
			writeError(w, http.StatusConflict, err)
			return
		}
		fmt.Printf("[auth] registration failed: %v\n", err)
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not create account"))
		return
	}

	token, err := GenerateToken(user.ID, user.Email, s.store.Secret())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	fmt.Printf("[auth] user registered: id=%d email=%s ip=%s\n", user.ID, user.Email, ip) // A09: logging
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": token,
		"user":  map[string]any{"id": user.ID, "email": user.Email, "name": user.Name},
	})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.rl.Allow("login:"+ip, 10, time.Minute) { // A07: rate limit
		writeError(w, http.StatusTooManyRequests, fmt.Errorf("too many login attempts, try again later"))
		return
	}

	var req loginRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if err := validateEmail(req.Email); err != nil || req.Password == "" {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("invalid email or password"))
		return
	}

	user, err := s.store.GetUserByEmail(req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("invalid email or password"))
			return
		}
		fmt.Printf("[auth] login lookup failed: %v\n", err)
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not sign in"))
		return
	}
	if err := CheckPassword(req.Password, user.PasswordHash); err != nil {
		fmt.Printf("[auth] failed login: email=%s ip=%s\n", req.Email, ip) // A09
		writeError(w, http.StatusUnauthorized, fmt.Errorf("invalid email or password"))
		return
	}

	token, err := GenerateToken(user.ID, user.Email, s.store.Secret())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	fmt.Printf("[auth] user logged in: id=%d email=%s ip=%s\n", user.ID, user.Email, ip) // A09
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  map[string]any{"id": user.ID, "email": user.Email, "name": user.Name},
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromCtx(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	user, err := s.store.GetUserByID(userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		fmt.Printf("[auth] user lookup failed: %v\n", err)
		writeError(w, http.StatusInternalServerError, fmt.Errorf("could not load account"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":    user.ID,
		"email": user.Email,
		"name":  user.Name,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	// JWT is stateless; client just discards the token.
	// In a production system you'd add the token to a denylist.
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}
