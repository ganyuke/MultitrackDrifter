package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr              string
	AppBaseURL        string
	DatabasePath      string
	CookieSecret      string
	SecureCookies     bool
	SessionTTL        time.Duration
	DevAuthEnabled    bool
	LDAP              LDAPConfig
	SourceAdapter     string
	SourceLocalRoot   string
	HLSAdapter        string
	HLSLocalRoot      string
	HLSLocalURLPrefix string
	HLSPresignTTL     time.Duration
	FFmpegBin         string
	FFprobeBin        string
	TranscodeProfile  string
}

type LDAPConfig struct {
	URL           string
	BindDN        string
	BindPassword  string
	UserBaseDN    string
	UserFilter    string
	GroupBaseDN   string
	CreatorGroups []string
}

func Load() (Config, error) {
	_ = LoadDotEnv(".env")
	cfg := Config{
		Addr:              getenv("ADDR", "127.0.0.1:8080"),
		AppBaseURL:        getenv("APP_BASE_URL", "http://127.0.0.1:8080"),
		DatabasePath:      getenv("DATABASE_PATH", "./storage/drifter.db"),
		CookieSecret:      getenv("COOKIE_SECRET", "dev-change-me-32-bytes-minimum"),
		SecureCookies:     getenvBool("SECURE_COOKIES", false),
		SessionTTL:        time.Duration(getenvInt("SESSION_TTL_HOURS", 12)) * time.Hour,
		DevAuthEnabled:    getenvBool("DEV_AUTH_ENABLED", true),
		SourceAdapter:     getenv("SOURCE_ADAPTER", "local"),
		SourceLocalRoot:   getenv("SOURCE_LOCAL_ROOT", "./storage/source"),
		HLSAdapter:        getenv("HLS_ADAPTER", "local"),
		HLSLocalRoot:      getenv("HLS_LOCAL_ROOT", "./storage/hls"),
		HLSLocalURLPrefix: strings.TrimRight(getenv("HLS_LOCAL_URL_PREFIX", "/media/hls"), "/"),
		HLSPresignTTL:     time.Duration(getenvInt("HLS_PRESIGN_TTL_SECONDS", 3600)) * time.Second,
		FFmpegBin:         getenv("FFMPEG_BIN", "ffmpeg"),
		FFprobeBin:        getenv("FFPROBE_BIN", "ffprobe"),
		TranscodeProfile:  getenv("TRANSCODE_PROFILE_VERSION", "poc-480p-v1"),
	}
	cfg.LDAP = LDAPConfig{
		URL:           getenv("LDAP_URL", ""),
		BindDN:        getenv("LDAP_BIND_DN", ""),
		BindPassword:  getenv("LDAP_BIND_PASSWORD", ""),
		UserBaseDN:    getenv("LDAP_USER_BASE_DN", ""),
		UserFilter:    getenv("LDAP_USER_FILTER", "(uid=%s)"),
		GroupBaseDN:   getenv("LDAP_GROUP_BASE_DN", ""),
		CreatorGroups: splitCSV(getenv("LDAP_CREATOR_GROUPS", "")),
	}
	if len(cfg.CookieSecret) < 32 {
		return Config{}, fmt.Errorf("COOKIE_SECRET must be at least 32 bytes")
	}
	return cfg, nil
}

func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
	return s.Err()
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getenvBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getenvInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
