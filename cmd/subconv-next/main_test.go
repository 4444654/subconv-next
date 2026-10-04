package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"subconv-next/internal/model"
)

func TestRunVersion(t *testing.T) {
	oldVersion := version
	version = "0.1.0-test"
	t.Cleanup(func() {
		version = oldVersion
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(version) exit code = %d, want 0", code)
	}

	if got := stdout.String(); got != "0.1.0-test\n" {
		t.Fatalf("stdout = %q, want %q", got, "0.1.0-test\n")
	}

	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"nope"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run(nope) exit code = %d, want 2", code)
	}

	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}

	if got := stderr.String(); !strings.Contains(got, "unknown command: nope") {
		t.Fatalf("stderr = %q, want unknown command message", got)
	}
}

func TestRunRootServeFlagsUseServeCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--bad-serve-flag"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run(root serve flags) exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr = %q, want serve command handling", stderr.String())
	}
}

func TestApplyServeOverrides(t *testing.T) {
	cfg := model.DefaultConfig()
	dir := t.TempDir()

	err := applyServeOverrides(&cfg, serveOverrides{
		host:              "0.0.0.0",
		port:              19876,
		dataDir:           dir,
		publicBaseURL:     "https://subconv.example.com/",
		logLevel:          "debug",
		accessToken:       "management-secret",
		publicConverter:   boolPointer(true),
		trustProxyHeaders: boolPointer(true),
	})
	if err != nil {
		t.Fatalf("applyServeOverrides() error = %v", err)
	}

	if cfg.Service.ListenAddr != "0.0.0.0" || cfg.Service.ListenPort != 19876 {
		t.Fatalf("listen = %s:%d, want override", cfg.Service.ListenAddr, cfg.Service.ListenPort)
	}
	if cfg.Service.StatePath != filepath.Join(dir, "state.json") ||
		cfg.Service.CacheDir != filepath.Join(dir, "cache") ||
		cfg.Service.OutputPath != filepath.Join(dir, "mihomo.yaml") {
		t.Fatalf("data paths = %#v, want under %s", cfg.Service, dir)
	}
	if cfg.Service.PublicBaseURL != "https://subconv.example.com" {
		t.Fatalf("PublicBaseURL = %q, want trimmed override", cfg.Service.PublicBaseURL)
	}
	if cfg.Service.LogLevel != "debug" || cfg.Render.LogLevel != "debug" {
		t.Fatalf("log levels = %q/%q, want debug", cfg.Service.LogLevel, cfg.Render.LogLevel)
	}
	if cfg.Service.AccessToken != "management-secret" || cfg.Service.SubscriptionToken != "management-secret" {
		t.Fatalf("access tokens = %q/%q, want environment override", cfg.Service.AccessToken, cfg.Service.SubscriptionToken)
	}
	if !cfg.Service.PublicConverter {
		t.Fatal("PublicConverter = false, want true")
	}
	if !cfg.Service.TrustProxyHeaders {
		t.Fatal("TrustProxyHeaders = false, want true")
	}
}

func TestLoadServeConfigMissingUsesDefaults(t *testing.T) {
	cfg, err := loadServeConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("loadServeConfig(missing) error = %v", err)
	}
	if cfg.Service.ListenPort != model.DefaultListenPort || cfg.Service.OutputPath != model.DefaultOutputPath {
		t.Fatalf("loadServeConfig(missing) = %+v, want defaults", cfg.Service)
	}
}

func TestServeOverridesFromEnvAndFlags(t *testing.T) {
	t.Setenv("SUBCONV_HOST", "0.0.0.0")
	t.Setenv("SUBCONV_PORT", "19876")
	t.Setenv("SUBCONV_DATA_DIR", "/tmp/subconv-data")
	t.Setenv("SUBCONV_PUBLIC_BASE_URL", "https://subconv.example.com")
	t.Setenv("SUBCONV_LOG_LEVEL", "debug")
	t.Setenv("SUBCONV_ACCESS_TOKEN", "management-secret")
	t.Setenv("SUBCONV_PUBLIC_CONVERTER", "true")
	t.Setenv("SUBCONV_TRUST_PROXY_HEADERS", "true")

	got, err := serveOverridesFromEnvAndFlags(map[string]bool{
		"port":      true,
		"log-level": true,
	}, serveOverrides{
		port:     9876,
		logLevel: "info",
	})
	if err != nil {
		t.Fatalf("serveOverridesFromEnvAndFlags() error = %v", err)
	}

	if got.host != "0.0.0.0" || got.port != 9876 || got.dataDir != "/tmp/subconv-data" ||
		got.publicBaseURL != "https://subconv.example.com" || got.logLevel != "info" || got.accessToken != "management-secret" ||
		got.publicConverter == nil || !*got.publicConverter || got.trustProxyHeaders == nil || !*got.trustProxyHeaders {
		t.Fatalf("serveOverridesFromEnvAndFlags() = %#v", got)
	}
}

func TestFalseBooleanEnvironmentOverridesConfiguredTrue(t *testing.T) {
	t.Setenv("SUBCONV_PUBLIC_CONVERTER", "false")
	t.Setenv("SUBCONV_TRUST_PROXY_HEADERS", "false")
	t.Setenv("SUBCONV_ALLOW_INSECURE_PUBLIC", "false")

	overrides, err := serveOverridesFromEnvAndFlags(map[string]bool{}, serveOverrides{})
	if err != nil {
		t.Fatalf("serveOverridesFromEnvAndFlags() error = %v", err)
	}
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	cfg.Service.TrustProxyHeaders = true
	cfg.Service.AllowInsecurePublic = true
	if err := applyServeOverrides(&cfg, overrides); err != nil {
		t.Fatalf("applyServeOverrides() error = %v", err)
	}
	if cfg.Service.PublicConverter || cfg.Service.TrustProxyHeaders || cfg.Service.AllowInsecurePublic {
		t.Fatalf("boolean overrides did not disable configured values: %+v", cfg.Service)
	}
}

func TestServeOverridesRejectInvalidBooleanEnvironment(t *testing.T) {
	t.Setenv("SUBCONV_PUBLIC_CONVERTER", "sometimes")
	if _, err := serveOverridesFromEnvAndFlags(map[string]bool{}, serveOverrides{}); err == nil || !strings.Contains(err.Error(), "SUBCONV_PUBLIC_CONVERTER") {
		t.Fatalf("serveOverridesFromEnvAndFlags() error = %v, want boolean validation failure", err)
	}
}

func boolPointer(value bool) *bool {
	return &value
}

func TestServeOverridesRejectInvalidEnvPort(t *testing.T) {
	t.Setenv("SUBCONV_PORT", "bad")
	if _, err := serveOverridesFromEnvAndFlags(map[string]bool{}, serveOverrides{}); err == nil || !strings.Contains(err.Error(), "SUBCONV_PORT") {
		t.Fatalf("serveOverridesFromEnvAndFlags() error = %v, want SUBCONV_PORT failure", err)
	}
}

func TestValidateServeSecurityRejectsWeakPublicToken(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AccessToken = "too-short"
	if err := validateServeSecurity(cfg); err == nil || !strings.Contains(err.Error(), "24 characters") {
		t.Fatalf("validateServeSecurity() error = %v, want minimum token length error", err)
	}

	cfg.Service.AccessToken = ""
	if err := validateServeSecurity(cfg); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("validateServeSecurity(tokenless non-loopback listener) error = %v, want token requirement", err)
	}

	cfg.Service.PublicConverter = true
	if err := validateServeSecurity(cfg); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("validateServeSecurity(public converter without token) error = %v, want token requirement", err)
	}
	cfg.Service.AccessToken = "a-strong-management-token"
	if err := validateServeSecurity(cfg); err != nil {
		t.Fatalf("validateServeSecurity(public converter with token) error = %v", err)
	}
	cfg.Service.PublicConverter = false

	cfg.Service.ListenAddr = "127.0.0.1"
	cfg.Service.AccessToken = "short-local-token"
	if err := validateServeSecurity(cfg); err != nil {
		t.Fatalf("validateServeSecurity(loopback) error = %v", err)
	}

	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AccessToken = ""
	cfg.Service.AllowInsecurePublic = true
	if err := validateServeSecurity(cfg); err != nil {
		t.Fatalf("validateServeSecurity(explicit insecure mode) error = %v", err)
	}
}

func TestSecureRuntimeDataTightensExistingPermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	nested := filepath.Join(root, "workspaces", "example")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	filePath := filepath.Join(nested, "config.json")
	if err := os.WriteFile(filePath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := secureRuntimeData(root); err != nil {
		t.Fatalf("secureRuntimeData() error = %v", err)
	}
	for _, path := range []string{root, filepath.Join(root, "workspaces"), nested} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("directory %q mode = %o, want 700", path, got)
		}
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", filePath, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o, want 600", got)
	}
}

func TestSecureRuntimeDataDoesNotFollowSymlinks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if err := secureRuntimeData(root); err != nil {
		t.Fatalf("secureRuntimeData() error = %v", err)
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatalf("Stat(outside) error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("outside file mode = %o, want 644", got)
	}
}

func TestRunParseJSON(t *testing.T) {
	inputPath := filepath.Join("..", "..", "testdata", "nodes", "vless-reality.txt")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"parse", "--input", inputPath, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(parse) exit code = %d, want 0; stderr=%q", code, stderr.String())
	}

	if got := stdout.String(); !strings.Contains(got, "\"type\": \"vless\"") {
		t.Fatalf("stdout = %q, want vless JSON output", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunGenerate(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	outputPath := filepath.Join(dir, "mihomo.yaml")

	configBody := `{
  "service": {
    "template": "lite"
  },
  "inline": [
    {
      "name": "manual",
      "content": "ss://YWVzLTI1Ni1nY206cGFzc0BleGFtcGxlLmNvbTo0NDM=#ss-node"
    }
  ]
}`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"generate", "--config", configPath, "--out", outputPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(generate) exit code = %d, want 0; stderr=%q", code, stderr.String())
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile(output) error = %v", err)
	}
	if got := string(data); !strings.Contains(got, "type: ss") {
		t.Fatalf("output = %q, want ss proxy", got)
	}
	if strings.TrimSpace(stdout.String()) != outputPath {
		t.Fatalf("stdout = %q, want %q", stdout.String(), outputPath)
	}
}

func TestPositiveIntEnvironmentOverrides(t *testing.T) {
	t.Setenv("SUBCONV_MAX_WORKSPACES", "32")
	t.Setenv("SUBCONV_MAX_PUBLICATIONS", "16")
	t.Setenv("SUBCONV_MAX_CONCURRENT_REFRESHES", "2")

	got, err := serveOverridesFromEnvAndFlags(map[string]bool{}, serveOverrides{})
	if err != nil {
		t.Fatalf("serveOverridesFromEnvAndFlags() error = %v", err)
	}
	if got.maxWorkspaces == nil || *got.maxWorkspaces != 32 ||
		got.maxPublications == nil || *got.maxPublications != 16 ||
		got.maxConcurrentRefreshes == nil || *got.maxConcurrentRefreshes != 2 {
		t.Fatalf("serveOverridesFromEnvAndFlags() = %#v", got)
	}

	cfg := model.DefaultConfig()
	if err := applyServeOverrides(&cfg, got); err != nil {
		t.Fatalf("applyServeOverrides() error = %v", err)
	}
	if cfg.Service.MaxWorkspaces != 32 || cfg.Service.MaxPublications != 16 || cfg.Service.MaxConcurrentRefreshes != 2 {
		t.Fatalf("resource limits after overrides = %+v", cfg.Service)
	}
}

func TestRegistrationEnvironmentOverride(t *testing.T) {
	t.Setenv("SUBCONV_REGISTRATION_ENABLED", "false")
	overrides, err := serveOverridesFromEnvAndFlags(map[string]bool{}, serveOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.DefaultConfig()
	if err := applyServeOverrides(&cfg, overrides); err != nil || cfg.Service.RegistrationEnabled {
		t.Fatal("explicitly disabled registration was not applied")
	}
	t.Setenv("SUBCONV_REGISTRATION_ENABLED", "invalid")
	if _, err := serveOverridesFromEnvAndFlags(map[string]bool{}, serveOverrides{}); err == nil {
		t.Fatal("invalid registration setting was accepted")
	}
}

func TestPositiveIntEnvironmentOverridesAreOptional(t *testing.T) {
	got, err := serveOverridesFromEnvAndFlags(map[string]bool{}, serveOverrides{})
	if err != nil {
		t.Fatalf("serveOverridesFromEnvAndFlags() error = %v", err)
	}
	if got.maxWorkspaces != nil || got.maxPublications != nil || got.maxConcurrentRefreshes != nil {
		t.Fatalf("unset environment produced overrides: %#v", got)
	}
}

func TestPositiveIntEnvironmentRejectsInvalidValues(t *testing.T) {
	t.Setenv("SUBCONV_MAX_WORKSPACES", "0")
	if _, err := serveOverridesFromEnvAndFlags(map[string]bool{}, serveOverrides{}); err == nil || !strings.Contains(err.Error(), "SUBCONV_MAX_WORKSPACES") {
		t.Fatalf("serveOverridesFromEnvAndFlags() error = %v, want SUBCONV_MAX_WORKSPACES validation error", err)
	}

	t.Setenv("SUBCONV_MAX_WORKSPACES", "many")
	if _, err := serveOverridesFromEnvAndFlags(map[string]bool{}, serveOverrides{}); err == nil || !strings.Contains(err.Error(), "SUBCONV_MAX_WORKSPACES") {
		t.Fatalf("serveOverridesFromEnvAndFlags() error = %v, want SUBCONV_MAX_WORKSPACES validation error", err)
	}
}
