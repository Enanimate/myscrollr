package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

type mockLogto struct {
	privateKey *rsa.PrivateKey
	keyID      string
}

func newMockLogto() *mockLogto {
	return &mockLogto{
		privateKey: generateRSAKey(),
		keyID:      "test-key-1",
	}
}

func generateRSAKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("Failed to generate RSA key: %v", err)
	}
	return key
}

func (m *mockLogto) jwksHandler(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"keys": []fiber.Map{
			{
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"kid": m.keyID,
				"n":   base64URLEncode(m.privateKey.N),
				"e":   base64URLEncode(big.NewInt(int64(m.privateKey.E))),
			},
		},
	})
}

func (m *mockLogto) tokenHandler(c *fiber.Ctx) error {
	if c.Method() != http.MethodPost {
		return c.SendStatus(http.StatusMethodNotAllowed)
	}

	clientID := c.FormValue("client_id")
	clientSecret := c.FormValue("client_secret")
	grantType := c.FormValue("grant_type")
	refreshToken := c.FormValue("refresh_token")

	if grantType == "client_credentials" {
		if clientID == "" || clientSecret == "" {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{
				"error":             "invalid_client",
				"error_description": "client_id and client_secret are required",
			})
		}
		subject := "mock-service-subject"
		token, expiresIn := m.generateJWT(subject, clientID)
		return c.JSON(fiber.Map{
			"access_token": token,
			"token_type":   "Bearer",
			"expires_in":    expiresIn,
		})
	}

	if grantType == "refresh_token" {
		if refreshToken == "" {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{
				"error":             "invalid_request",
				"error_description": "refresh_token is required",
			})
		}
		token, expiresIn := m.generateJWT(refreshToken, "mock-client")
		return c.JSON(fiber.Map{
			"access_token":  token,
			"token_type":    "Bearer",
			"expires_in":    expiresIn,
			"refresh_token": refreshToken + "-rotated",
		})
	}

	return c.Status(http.StatusBadRequest).JSON(fiber.Map{
		"error":             "unsupported_grant_type",
		"error_description":  fmt.Sprintf("grant_type '%s' is not supported", grantType),
	})
}

func (m *mockLogto) generateJWT(subject, audience string) (string, int) {
	now := time.Now()
	expiresIn := 3600
	exp := now.Add(time.Duration(expiresIn) * time.Second)

	header := base64URLEncodeNoPadding([]byte(
		`{"alg":"RS256","typ":"JWT","kid":"` + m.keyID + `"}`,
	))
	payload := base64URLEncodeNoPadding([]byte(fmt.Sprintf(
		`{"sub":"%s","aud":"%s","iat":%d,"exp":%d,"jti":"%s"}`,
		subject, audience, now.Unix(), exp.Unix(), randomID(),
	)))

	digest := sha256.Sum256([]byte(header + "." + payload))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		log.Fatalf("Failed to sign JWT: %v", err)
	}
	signature := base64URLEncodeNoPadding(sig)

	return header + "." + payload + "." + signature, expiresIn
}

func (m *mockLogto) userRolesHandler(c *fiber.Ctx) error {
	userID := c.Params("id")
	if userID == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": "user id required"})
	}

	switch c.Method() {
	case http.MethodPost:
		var req struct {
			RoleIDs []string `json:"roleIds"`
		}
		if err := c.BodyParser(&req); err != nil {
			log.Printf("[mock-logto] Failed to parse role request: %v", err)
		}
		log.Printf("[mock-logto] Assigned roles to user %s: %v", userID, req.RoleIDs)
		_, _ = json.Marshal(req.RoleIDs)
		return c.JSON(fiber.Map{
			"id":        userID,
			"roleIds":   req.RoleIDs,
			"updatedAt": time.Now().Format(time.RFC3339),
		})

	case http.MethodDelete:
		var req struct {
			RoleIDs []string `json:"roleIds"`
		}
		if err := c.BodyParser(&req); err != nil {
			log.Printf("[mock-logto] Failed to parse delete role request: %v", err)
		}
		log.Printf("[mock-logto] Removed roles from user %s: %v", userID, req.RoleIDs)
		return c.JSON(fiber.Map{
			"id":        userID,
			"roleIds":   []string{},
			"updatedAt": time.Now().Format(time.RFC3339),
		})
	}

	return c.SendStatus(http.StatusMethodNotAllowed)
}

func (m *mockLogto) healthHandler(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok", "service": "mock-logto"})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9001"
	}

	m := newMockLogto()
	log.Printf("[mock-logto] Test RSA key generated (kid=%s)", m.keyID)

	app := fiber.New()
	app.Use(recover.New())
	app.Use(logger.New())

	app.Get("/health", m.healthHandler)
	app.Get("/oidc/jwks", m.jwksHandler)
	app.Post("/oidc/token", m.tokenHandler)
	app.Post("/api/users/:id/roles", m.userRolesHandler)
	app.Delete("/api/users/:id/roles", m.userRolesHandler)

	log.Printf("[mock-logto] Listening on :%s", port)
	log.Fatalf("[mock-logto] Server error: %v", app.Listen(":"+port))
}

func base64URLEncode(n *big.Int) string {
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(n.Bytes())
}

func base64URLEncodeNoPadding(data []byte) string {
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(data)
}

func randomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b)
}
