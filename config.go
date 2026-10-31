package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Listen    string
	DataDir   string
	AdminHash string
	// BaseURL is the public origin (https://blog.example.com). RSS uses it
	// for absolute links, and form posts may come from it.
	BaseURL string
	// TrustProxy makes the app read the client IP from CF-Connecting-IP.
	TrustProxy bool
	// Bluesky account that announces new posts; both empty turns it off.
	BlueskyHandle, BlueskyPassword string
	BlueskyService                 string // the account's server, https://bsky.social
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func loadConfig() (Config, error) {
	c := Config{
		Listen:     env("LISTEN_ADDR", ":8080"),
		DataDir:    env("DATA_DIR", "./data"),
		AdminHash:  env("ADMIN_PASSWORD_HASH", ""),
		BaseURL:    strings.TrimRight(env("BASE_URL", ""), "/"),
		TrustProxy: env("TRUST_PROXY_HEADERS", "true") != "false",

		BlueskyHandle:   strings.TrimPrefix(env("BLUESKY_HANDLE", ""), "@"),
		BlueskyPassword: env("BLUESKY_APP_PASSWORD", ""),
		BlueskyService:  env("BLUESKY_SERVICE", "https://bsky.social"),
	}
	if c.AdminHash == "" {
		return c, errors.New("ADMIN_PASSWORD_HASH is not set; generate it with: blog hashpw")
	}
	if _, err := parseHash(c.AdminHash); err != nil {
		return c, fmt.Errorf("ADMIN_PASSWORD_HASH: %w", err)
	}
	return c, nil
}
