package config

import (
	"flag"
	"io"
	"strconv"
)

type Config struct {
	Port   int
	DBPath string
	Dev    bool
	// UpdateRepo is the GitHub "owner/name" polled for releases. Empty
	// disables update checks entirely (REVERB_UPDATE_REPO=off).
	UpdateRepo string
}

// DefaultUpdateRepo is the repository update checks use when
// REVERB_UPDATE_REPO is unset.
const DefaultUpdateRepo = "uhhhm/reverb"

// Load resolves config: flags win over env, env wins over defaults.
func Load(args []string, getenv func(string) string) (Config, error) {
	c := Config{Port: 8090, DBPath: "./data/reverb.db", UpdateRepo: DefaultUpdateRepo}

	if v := getenv("REVERB_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.Port = p
		}
	}
	if v := getenv("REVERB_DB"); v != "" {
		c.DBPath = v
	}
	if getenv("REVERB_DEV") == "1" {
		c.Dev = true
	}
	if v := getenv("REVERB_UPDATE_REPO"); v != "" {
		c.UpdateRepo = v
	}

	fs := flag.NewFlagSet("reverb", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.IntVar(&c.Port, "port", c.Port, "HTTP port")
	fs.StringVar(&c.DBPath, "db", c.DBPath, "SQLite path")
	fs.BoolVar(&c.Dev, "dev", c.Dev, "dev mode (proxy Vite)")
	fs.StringVar(&c.UpdateRepo, "update-repo", c.UpdateRepo, `GitHub owner/name to check for updates ("off" disables)`)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if c.UpdateRepo == "off" {
		c.UpdateRepo = ""
	}
	return c, nil
}
