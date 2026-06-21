package main

import (
	"testing"

	"ai-localbase/internal/model"
)

func TestValidateAuthConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  model.ServerConfig
		wantErr string
	}{
		{
			name: "auth disabled",
			config: model.ServerConfig{
				EnableAuth: false,
			},
		},
		{
			name: "auth enabled enters setup without password",
			config: model.ServerConfig{
				EnableAuth: true,
			},
		},
		{
			name: "auth enabled accepts password without jwt secret",
			config: model.ServerConfig{
				EnableAuth:   true,
				AuthPassword: "password",
			},
		},
		{
			name: "legacy jwt secret is optional",
			config: model.ServerConfig{
				EnableAuth:   true,
				AuthPassword: "password",
				JWTSecret:    "short",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAuthConfig(tt.config)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %q, got nil", tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("expected error %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}
