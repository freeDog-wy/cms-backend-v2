package main

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixture() map[string]string {
	return map[string]string{
		"IMAGE_REF":              "ccr.ccs.tencentyun.com/pigpig/cms-backend-v2@sha256:" + strings.Repeat("a", 64),
		"POSTGRES_PASSWORD":      "test-only$${DO_NOT_EXPAND}'double\"back\\slash\\",
		"CMS_TRUSTED_ORIGIN_URL": "https://www.example.com", "BACKEND_ORIGIN": "https://api.example.com",
		"CMS_ADMIN_PASSWORD": "  test-only$${DO_NOT_EXPAND}'double\"back\\slash\\",
		"CMS_SMTP_HOST":      "smtp.example.com", "CMS_SMTP_USERNAME": "sender@example.com",
		"CMS_SMTP_PASSWORD": "smtp$${DO_NOT_EXPAND}'double\"back\\slash\\",
		"CMS_SMTP_FROM":     "\"ElseIf Site Administrator: FreeDog_wy\" <sender@example.com>",
	}
}

func TestEnvironmentRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"POSTGRES_PASSWORD", ""}, {"CMS_ADMIN_PASSWORD", ""}, {"CMS_SMTP_PASSWORD", ""},
		{"CMS_ADMIN_PASSWORD", "test\nINJECT=1"}, {"CMS_SMTP_FROM", "a name without mailbox"},
		{"CMS_SMTP_FROM", "bad\r\nheader"}, {"CMS_SMTP_HOST", "smtp.example.com:465"},
		{"CMS_SMTP_TLS_MODE", "plain"}, {"CMS_SMTP_MAX_CONCURRENCY", "-1"},
		{"CMS_SMTP_PORT", "65536"}, {"CMS_SMTP_CONNECT_TIMEOUT", "oops"},
		{"CMS_ADMIN_EMAIL", "not-an-email"}, {"CMS_TRUSTED_ORIGIN_URL", "http://example.com"},
		{"CMS_TRUSTED_ORIGIN_URL", "https://example.com/path"}, {"BACKEND_ORIGIN", "https://example.com/?a=1"},
		{"CMS_TRUSTED_ORIGIN_URL", "https://example.com/?"},
		{"IMAGE_REF", "ccr.ccs.tencentyun.com/pigpig/cms-backend-v2:latest"},
		{"BACKEND_HOST_PORT", "0"}, {"BACKEND_HOST_PORT", "65536"}, {"POSTGRES_DB", "db/other"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			values := fixture()
			values[tc.key] = tc.value
			if _, err := environment(func(key string) string { return values[key] }); err == nil {
				t.Fatal("invalid environment accepted")
			}
		})
	}
}

func TestComposeRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker Compose CLI not installed")
	}
	input := fixture()
	path := filepath.Join(t.TempDir(), "production.env")
	if err := render(path, func(key string) string { return input[key] }); err != nil {
		t.Fatal(err)
	}
	var processEnv []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "CMS_") || strings.HasPrefix(key, "COMPOSE_") || strings.HasPrefix(key, "POSTGRES_") || key == "IMAGE_REF" || key == "BACKEND_HOST_PORT" || key == "BACKEND_ORIGIN" {
			continue
		}
		processEnv = append(processEnv, entry)
	}
	composeFile := filepath.Join("..", "docker-compose.yml")
	compose := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command("docker", append([]string{"compose", "--env-file", path, "-f", composeFile, "config"}, args...)...)
		cmd.Env = processEnv
		var stderr strings.Builder
		cmd.Stderr = &stderr
		data, err := cmd.Output()
		if err != nil {
			t.Fatalf("compose validation failed: %v: %s", err, stderr.String())
		}
		return data
	}
	data := compose("--format", "json")
	var cfg struct {
		Name     string
		Services map[string]struct {
			Environment map[string]string
			ReadOnly    bool `json:"read_only"`
			Ports       []struct {
				HostIP    string `json:"host_ip"`
				Published string
				Target    int
			}
			DependsOn map[string]struct{ Condition string } `json:"depends_on"`
		}
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "cms-backend-v2" {
		t.Fatal("production project is not isolated")
	}
	server := cfg.Services["server"]
	if !server.ReadOnly || len(server.Ports) != 1 || server.Ports[0].HostIP != "127.0.0.1" || server.Ports[0].Published != "8082" || server.Ports[0].Target != 8080 {
		t.Fatal("incorrect server runtime or port configuration")
	}
	if len(cfg.Services["postgres"].Ports) != 0 {
		t.Fatal("database exposed to host")
	}
	if len(server.DependsOn) != 1 || server.DependsOn["postgres"].Condition != "service_healthy" {
		t.Fatal("incorrect startup ordering")
	}
	if _, exists := cfg.Services["migrate"]; exists || len(cfg.Services) != 2 {
		t.Fatal("production Compose must contain only postgres and server")
	}
	vars := make(map[string]string)
	for _, line := range strings.Split(string(compose("--environment")), "\n") {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "=")
		if ok {
			vars[key] = value
		}
	}
	for _, key := range []string{"POSTGRES_PASSWORD", "CMS_ADMIN_PASSWORD", "CMS_SMTP_PASSWORD", "CMS_SMTP_FROM"} {
		if vars[key] != input[key] {
			t.Fatalf("%s changed during dotenv parsing", key)
		}
		service := "server"
		if key == "POSTGRES_PASSWORD" {
			service = "postgres"
		}
		if strings.ReplaceAll(cfg.Services[service].Environment[key], "$$", "$") != input[key] {
			t.Fatalf("%s changed during service environment rendering", key)
		}
	}
	u, err := url.Parse(strings.ReplaceAll(server.Environment["CMS_DATABASE_URL"], "$$", "$"))
	if err != nil {
		t.Fatal("invalid database DSN")
	}
	password, _ := u.User.Password()
	if password != input["POSTGRES_PASSWORD"] {
		t.Fatal("database password did not survive URL encoding")
	}
	if server.Environment["CMS_MODE"] != "PROD" || server.Environment["CMS_EMAIL_DRIVER"] != "smtp" || server.Environment["CMS_ADMIN_EMAIL"] != "372525247@qq.com" {
		t.Fatal("incorrect production configuration")
	}
}
