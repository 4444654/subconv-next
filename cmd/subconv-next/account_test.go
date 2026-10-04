package main

import (
	"bytes"
	"strings"
	"testing"

	"subconv-next/internal/authn"
	"subconv-next/internal/model"
)

func TestPasswordHashCommandKeepsPasswordOutOfOutput(t *testing.T) {
	var output, errors bytes.Buffer
	const password = "My independent passphrase!"
	if code := runHashPassword(nil, strings.NewReader(password), &output, &errors); code != 0 {
		t.Fatalf("hash-password returned %d: %s", code, errors.String())
	}
	if strings.Contains(output.String()+errors.String(), password) || !authn.VerifyPassword(strings.TrimSpace(output.String()), password) {
		t.Fatal("password was leaked or not hashed correctly")
	}
	for _, input := range []string{"short", password + "\n", strings.Repeat("x", 73)} {
		output.Reset()
		errors.Reset()
		if code := runHashPassword(nil, strings.NewReader(input), &output, &errors); code == 0 || output.Len() != 0 {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestAccountEnvironmentOverridesAndPublicSecurity(t *testing.T) {
	hash, err := authn.HashPassword("Independent password!")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUBCONV_MANAGEMENT_USERNAME", "operator")
	t.Setenv("SUBCONV_MANAGEMENT_PASSWORD_HASH", hash)
	overrides, err := serveOverridesFromEnvAndFlags(map[string]bool{}, serveOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.DefaultConfig()
	if err := applyServeOverrides(&cfg, overrides); err != nil {
		t.Fatal(err)
	}
	if cfg.Service.ManagementUsername != "operator" || cfg.Service.ManagementPasswordHash != hash {
		t.Fatal("account overrides were not applied")
	}
	cfg.Service.ListenAddr = "0.0.0.0"
	if err := validateServeSecurity(cfg); err != nil {
		t.Fatalf("valid account-only listener rejected: %v", err)
	}
	cfg.Service.AccessToken = "weak"
	if err := validateServeSecurity(cfg); err == nil {
		t.Fatal("weak API token allowed alongside account")
	}
}
