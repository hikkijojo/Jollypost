package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Port          string
	DBHost        string
	DBPort        string
	DBUser        string
	DBPass        string
	DBName        string
	LiffID        string
	LiffChannelID string
	UploadDir     string
	EnvFile       string
}

func Load() Config {
	return Config{
		Port:          getEnv("PORT", "8080"),
		DBHost:        getEnv("DB_HOST", "db"),
		DBPort:        getEnv("DB_PORT", "3306"),
		DBUser:        getEnv("DB_USER", "jingfeng"),
		DBPass:        os.Getenv("DB_PASSWORD"),
		DBName:        getEnv("DB_NAME", "jingfeng"),
		LiffID:        strings.TrimSpace(os.Getenv("LIFF_ID")),
		LiffChannelID: strings.TrimSpace(os.Getenv("LIFF_CHANNEL_ID")),
		UploadDir:     getEnv("UPLOAD_DIR", "/data/uploads"),
		EnvFile:       getEnv("ENV_FILE", "/app/.env"),
	}
}

func (c Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&loc=Asia%%2FTaipei&clientFoundRows=true",
		url.QueryEscape(c.DBUser),
		url.QueryEscape(c.DBPass),
		c.DBHost,
		c.DBPort,
		c.DBName,
	)
}

type Lists struct {
	Allowed map[string]struct{}
	Admin   map[string]struct{}
}

var listCache struct {
	sync.Mutex
	at    time.Time
	mtime time.Time
	lists Lists
}

func CurrentAdmins(envFile string) map[string]struct{} {
	return CurrentLists(envFile).Admin
}

func LegacyAllowed(envFile string) map[string]struct{} {
	return CurrentLists(envFile).Allowed
}

func CurrentLists(envFile string) Lists {
	listCache.Lock()
	defer listCache.Unlock()
	info, err := os.Stat(envFile)
	mtime := time.Time{}
	if err == nil {
		mtime = info.ModTime()
	}
	if time.Since(listCache.at) < 2*time.Second && mtime.Equal(listCache.mtime) {
		return listCache.lists
	}
	allowed, admin := readLists(envFile)
	listCache.at = time.Now()
	listCache.mtime = mtime
	listCache.lists = Lists{Allowed: allowed, Admin: admin}
	return listCache.lists
}

func readLists(envFile string) (map[string]struct{}, map[string]struct{}) {
	vals := map[string]string{
		"LINE_ALLOWED_USER_IDS": os.Getenv("LINE_ALLOWED_USER_IDS"),
		"LINE_ADMIN_USER_IDS":   os.Getenv("LINE_ADMIN_USER_IDS"),
	}
	if b, err := os.ReadFile(envFile); err == nil {
		b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(strings.TrimRight(line, "\r"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, val, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			if key == "LINE_ALLOWED_USER_IDS" || key == "LINE_ADMIN_USER_IDS" {
				vals[key] = strings.TrimSpace(val)
			}
		}
	}
	return toSet(vals["LINE_ALLOWED_USER_IDS"]), toSet(vals["LINE_ADMIN_USER_IDS"])
}

func toSet(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[part] = struct{}{}
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
