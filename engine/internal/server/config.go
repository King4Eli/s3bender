package server

import (
	"os"
	"strconv"
)

// Config is read from S3BENDER_* environment variables - see .env/engine.env.example and
// _docs/deployment.md for what each one does.
type Config struct {
	APIPort      int
	FrontendPort int

	StorageRoot string
	DBPath      string

	AdminAPIKey string
	MasterKey   string

	// Origin used when building presigned URLs. Empty means "whatever host the request targeted".
	PublicBaseURL string

	ClockSkewSeconds        int64
	MaxPresignExpirySeconds int64
}

func ConfigFromEnv() Config {
	return Config{
		APIPort:                 int(envInt("PORT", 8080)),
		FrontendPort:            int(envInt("S3BENDER_FRONTEND_PORT", 8081)),
		StorageRoot:             envStr("S3BENDER_STORAGE_ROOT", "./data/objects"),
		DBPath:                  envStr("S3BENDER_DB_PATH", "./data/db/s3bender.db"),
		AdminAPIKey:             os.Getenv("S3BENDER_ADMIN_API_KEY"),
		MasterKey:               os.Getenv("S3BENDER_MASTER_KEY"),
		PublicBaseURL:           os.Getenv("S3BENDER_PUBLIC_BASE_URL"),
		ClockSkewSeconds:        envInt("S3BENDER_CLOCK_SKEW_SECONDS", 900),
		MaxPresignExpirySeconds: envInt("S3BENDER_MAX_PRESIGN_EXPIRY_SECONDS", 604800),
	}
}

func envStr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func envInt(name string, fallback int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(name), 10, 64); err == nil {
		return v
	}
	return fallback
}
