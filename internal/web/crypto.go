package web

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// HashPassword hashes a password using bcrypt.
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword compares a plaintext password against a bcrypt hash.
func CheckPassword(password, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// Encrypt encrypts plaintext using AES-256-GCM and returns a base64 string.
func Encrypt(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decodes a base64 ciphertext and decrypts it with AES-256-GCM.
func Decrypt(encoded string, key []byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:ns], data[ns:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// GenerateToken creates a signed JWT for the given user ID.
func GenerateToken(userID int, email string, secret []byte) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   strconv.Itoa(userID),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		Issuer:    "myplatform",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ParseToken validates a JWT and returns the user ID.
func ParseToken(tokenString string, secret []byte) (int, error) {
	token, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return secret, nil
		})
	if err != nil {
		return 0, err
	}
	if claims, ok := token.Claims.(*jwt.RegisteredClaims); ok && token.Valid {
		return strconv.Atoi(claims.Subject)
	}
	return 0, fmt.Errorf("invalid token claims")
}
