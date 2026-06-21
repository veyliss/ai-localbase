package auth

import (
	"strings"
	"testing"
	"time"
)

func preserveJWTState(t *testing.T) {
	t.Helper()
	previousSecret := append([]byte(nil), jwtSecret...)
	previousUsername := configuredUsername
	t.Cleanup(func() {
		jwtSecret = previousSecret
		configuredUsername = previousUsername
	})
}

func TestJWTRequiresInitializedSecret(t *testing.T) {
	preserveJWTState(t)
	jwtSecret = nil
	configuredUsername = ""

	if _, err := GenerateToken("admin", time.Hour); err == nil {
		t.Fatal("expected GenerateToken to fail without jwt secret")
	}
	if _, err := ValidateToken("header.claims.signature"); err == nil {
		t.Fatal("expected ValidateToken to fail without jwt secret")
	}
}

func TestJWTSecretAndUsernameAreTrimmed(t *testing.T) {
	preserveJWTState(t)

	InitJWTSecret(" "+strings.Repeat("s", 32)+" ", " admin ")

	token, err := GenerateToken("admin", time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	claims, err := ValidateToken(token)
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}
	if claims.Username != "admin" {
		t.Fatalf("expected username admin, got %q", claims.Username)
	}
}

func TestValidateTokenRejectsUnexpectedUsername(t *testing.T) {
	preserveJWTState(t)

	InitJWTSecret(strings.Repeat("s", 32), "admin")

	token, err := GenerateToken("other-user", time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	if _, err := ValidateToken(token); err == nil || err.Error() != "invalid subject" {
		t.Fatalf("expected invalid subject error, got %v", err)
	}
}

func TestHasRequiredScopes(t *testing.T) {
	if !hasRequiredScopes([]string{"openai:chat"}, []string{"openai:chat"}) {
		t.Fatal("expected matching scope to be accepted")
	}
	if hasRequiredScopes([]string{"mcp:tools"}, []string{"openai:chat"}) {
		t.Fatal("expected missing scope to be rejected")
	}
	if !hasRequiredScopes([]string{"mcp:tools"}, nil) {
		t.Fatal("expected empty required scopes to be accepted")
	}
}
