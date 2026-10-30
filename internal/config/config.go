package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
	"github.com/freeDog-wy/cms-backend-v2/internal/pkg"
)

type Config struct {
	Mode string `env:"CMS_MODE" envDefault:"DEV"` // "DEV"、"TEST"、"PROD"

	TrustedOriginURL string `env:"CMS_TRUSTED_ORIGIN_URL" envDefault:"http://localhost:5173"`

	// TrustedProxiesIP []string `env:"CMS_TRUSTED_PROXIES_IP" envDefault:"127.0.0.1,::1"`                    // 代理服务器地址
	// RemoteIPHeaders  []string `env:"CMS_REMOTE_IP_HEADERS"  envDefault:"CF-Connecting-IP,X-Forwarded-For"` // 代理服务器转发的真实客户IP存的请求头的键

	DatabaseURL string `env:"CMS_DATABASE_URL,required"`

	AdminName     string `env:"CMS_ADMIN_NAME"     envDefault:"admin"`
	AdminPassword string `env:"CMS_ADMIN_PASSWORD" envDefault:"123456"`
	AdminEmail    string `env:"CMS_ADMIN_EMAIL"    envDefault:"admin@example.com"`

	EmailDriver string              `env:"CMS_EMAIL_DRIVER" envDefault:"log"` // log、smtp；PROD 必须使用 smtp。
	SMTP        pkg.SMTPEmailConfig `envPrefix:"CMS_SMTP_"`
}

func (cfg *Config) Valid() error {
	if cfg.Mode != "DEV" && cfg.Mode != "TEST" && cfg.Mode != "PROD" {
		return fmt.Errorf("cfg mode must be DEV or TEST or PROD")
	}
	if cfg.EmailDriver != "log" && cfg.EmailDriver != "smtp" {
		return fmt.Errorf("email driver must be log or smtp")
	}
	if cfg.Mode == "PROD" && cfg.EmailDriver != "smtp" {
		return fmt.Errorf("PROD requires CMS_EMAIL_DRIVER=smtp")
	}
	return nil
}

func Load() (*Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Valid(); err != nil {
		return nil, err
	}

	return &cfg, nil
}
