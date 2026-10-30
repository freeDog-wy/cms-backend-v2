// render-env produces a release-specific Compose environment file without printing credentials.
package main

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/caarlos0/env/v11"
	"github.com/freeDog-wy/cms-backend-v2/internal/config"
	"github.com/freeDog-wy/cms-backend-v2/internal/pkg"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: render-env OUTPUT_FILE")
		os.Exit(1)
	}
	if err := render(os.Args[1], os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "Environment generation failed:", err)
		os.Exit(1)
	}
}

func environment(get func(string) string) (map[string]string, error) {
	values := make(map[string]string)
	for _, key := range []string{"IMAGE_REF", "POSTGRES_PASSWORD", "CMS_TRUSTED_ORIGIN_URL", "BACKEND_ORIGIN", "CMS_ADMIN_PASSWORD", "CMS_SMTP_HOST", "CMS_SMTP_USERNAME", "CMS_SMTP_PASSWORD", "CMS_SMTP_FROM"} {
		value := get(key)
		if value == "" || strings.ContainsAny(value, "\r\n\x00") {
			return nil, fmt.Errorf("%s must be set and be a single line", key)
		}
		values[key] = value
	}
	defaults := map[string]string{
		"POSTGRES_USER": "cms", "POSTGRES_DB": "cms", "BACKEND_HOST_PORT": "8082",
		"CMS_ADMIN_NAME": "admin", "CMS_ADMIN_EMAIL": "372525247@qq.com",
		"CMS_SMTP_PORT": "0", "CMS_SMTP_TLS_MODE": "tls", "CMS_SMTP_MAX_CONCURRENCY": "3",
		"CMS_SMTP_CONNECT_TIMEOUT": "10s", "CMS_SMTP_SEND_TIMEOUT": "20s",
	}
	for key, fallback := range defaults {
		values[key] = get(key)
		if values[key] == "" {
			values[key] = fallback
		}
	}
	if !regexp.MustCompile(`^ccr\.ccs\.tencentyun\.com/[a-z0-9._-]+/[a-z0-9._-]+@sha256:[0-9a-f]{64}$`).MatchString(values["IMAGE_REF"]) {
		return nil, fmt.Errorf("IMAGE_REF must be a Tencent CCR image digest")
	}
	identifier := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	if !identifier.MatchString(values["POSTGRES_USER"]) || !identifier.MatchString(values["POSTGRES_DB"]) {
		return nil, fmt.Errorf("POSTGRES_USER and POSTGRES_DB must be simple identifiers")
	}
	for _, key := range []string{"CMS_TRUSTED_ORIGIN_URL", "BACKEND_ORIGIN"} {
		u, err := url.Parse(values[key])
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("%s must be an HTTPS origin", key)
		}
		values[key] = strings.TrimSuffix(values[key], "/")
	}
	port, err := strconv.Atoi(values["BACKEND_HOST_PORT"])
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("BACKEND_HOST_PORT must be 1-65535")
	}
	name := values["CMS_ADMIN_NAME"]
	if len([]rune(name)) < 3 || len([]rune(name)) > 64 || strings.TrimSpace(name) != name || strings.ContainsFunc(name, unicode.IsControl) {
		return nil, fmt.Errorf("CMS_ADMIN_NAME must be a trimmed 3-64 character name")
	}
	address, err := mail.ParseAddress(values["CMS_ADMIN_EMAIL"])
	if err != nil || strings.ContainsFunc(values["CMS_ADMIN_EMAIL"], unicode.IsControl) || strings.ContainsFunc(address.Address, func(r rune) bool { return r > 127 }) {
		return nil, fmt.Errorf("CMS_ADMIN_EMAIL must be a valid mailbox")
	}
	databaseURL := &url.URL{Scheme: "postgres", User: url.UserPassword(values["POSTGRES_USER"], values["POSTGRES_PASSWORD"]), Host: "postgres:5432", Path: "/" + values["POSTGRES_DB"], RawQuery: "sslmode=disable"}
	values["CMS_DATABASE_URL"] = databaseURL.String()
	values["CMS_MODE"], values["CMS_EMAIL_DRIVER"] = "PROD", "smtp"
	for key, value := range values {
		if strings.ContainsAny(value, "\r\n\x00") {
			return nil, fmt.Errorf("%s must be a single line", key)
		}
	}
	// Validate with the same parser and constructor used by the server. No SMTP connection is made.
	cfg, err := env.ParseAsWithOptions[config.Config](env.Options{Environment: values})
	if err != nil {
		return nil, fmt.Errorf("invalid typed CMS configuration")
	}
	if err := cfg.Valid(); err != nil {
		return nil, err
	}
	if _, err := pkg.NewSMTPEmailSender(cfg.SMTP); err != nil {
		return nil, err
	}
	return values, nil
}

func render(path string, get func(string) string) error {
	values, err := environment(get)
	if err != nil {
		return err
	}
	var content strings.Builder
	// Explicit order keeps release diffs readable and excludes unrelated runner environment variables.
	for _, key := range []string{
		"IMAGE_REF", "POSTGRES_USER", "POSTGRES_DB", "POSTGRES_PASSWORD", "CMS_DATABASE_URL", "BACKEND_HOST_PORT", "BACKEND_ORIGIN",
		"CMS_MODE", "CMS_TRUSTED_ORIGIN_URL", "CMS_ADMIN_NAME", "CMS_ADMIN_PASSWORD", "CMS_ADMIN_EMAIL", "CMS_EMAIL_DRIVER",
		"CMS_SMTP_HOST", "CMS_SMTP_PORT", "CMS_SMTP_TLS_MODE", "CMS_SMTP_USERNAME", "CMS_SMTP_PASSWORD", "CMS_SMTP_FROM",
		"CMS_SMTP_MAX_CONCURRENCY", "CMS_SMTP_CONNECT_TIMEOUT", "CMS_SMTP_SEND_TIMEOUT",
	} {
		quoted := strings.ReplaceAll(strconv.Quote(values[key]), "$", `\$`)
		fmt.Fprintf(&content, "%s=%s\n", key, quoted)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		return errors.Join(err, file.Close())
	}
	_, err = file.WriteString(content.String())
	return errors.Join(err, file.Close())
}
