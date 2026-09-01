package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	c, err := Load(nil, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 8090 || c.DBPath != "./data/reverb.db" || c.Dev {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadFlagsOverrideDefaults(t *testing.T) {
	c, err := Load([]string{"--port", "9000", "--dev"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 9000 || !c.Dev {
		t.Fatalf("flags not applied: %+v", c)
	}
}

func TestEnvFillsPortWhenNoFlag(t *testing.T) {
	env := map[string]string{"REVERB_PORT": "7000"}
	c, err := Load(nil, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 7000 {
		t.Fatalf("env not applied: %+v", c)
	}
}

func TestUpdateRepo(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{name: "default", want: DefaultUpdateRepo},
		{name: "env", env: map[string]string{"REVERB_UPDATE_REPO": "me/fork"}, want: "me/fork"},
		{name: "flag beats env", args: []string{"--update-repo", "me/fork"},
			env: map[string]string{"REVERB_UPDATE_REPO": "other/fork"}, want: "me/fork"},
		{name: "off disables", env: map[string]string{"REVERB_UPDATE_REPO": "off"}, want: ""},
		{name: "off via flag", args: []string{"--update-repo", "off"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Load(tt.args, func(k string) string { return tt.env[k] })
			if err != nil {
				t.Fatal(err)
			}
			if c.UpdateRepo != tt.want {
				t.Fatalf("UpdateRepo = %q, want %q", c.UpdateRepo, tt.want)
			}
		})
	}
}

func TestBindAddrDefaultsToLoopback(t *testing.T) {
	c, err := Load(nil, func(string) string { return "" })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.BindAddr != DefaultBindAddr {
		t.Fatalf("BindAddr = %q, want %q", c.BindAddr, DefaultBindAddr)
	}
}

func TestBindAddrEnvAndFlag(t *testing.T) {
	env := func(k string) string {
		if k == "REVERB_BIND" {
			return "0.0.0.0"
		}
		return ""
	}
	c, err := Load(nil, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.BindAddr != "0.0.0.0" {
		t.Fatalf("env BindAddr = %q, want 0.0.0.0", c.BindAddr)
	}
	// Flags win over env.
	c, err = Load([]string{"--bind", "10.0.0.5"}, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.BindAddr != "10.0.0.5" {
		t.Fatalf("flag BindAddr = %q, want 10.0.0.5", c.BindAddr)
	}
}

func TestBindAddrUnbracketsIPv6(t *testing.T) {
	// Users write the bracketed form because that is how an IPv6 host appears
	// in host:port, but net.JoinHostPort re-brackets it, so Load must not.
	c, err := Load([]string{"--bind", "[::1]"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if c.BindAddr != "::1" {
		t.Errorf("BindAddr = %q, want %q", c.BindAddr, "::1")
	}
	c, err = Load(nil, func(k string) string {
		if k == "REVERB_BIND" {
			return "[fd00::1]"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.BindAddr != "fd00::1" {
		t.Errorf("BindAddr = %q, want %q", c.BindAddr, "fd00::1")
	}
}

func TestAllowNetworkAccessDefaultsOff(t *testing.T) {
	c, err := Load(nil, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if c.AllowNetworkAccess {
		t.Error("AllowNetworkAccess defaulted to true; a widened bind must be a deliberate act")
	}
}

func TestAllowNetworkAccessFlagAndEnv(t *testing.T) {
	c, err := Load([]string{"--allow-network-access"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !c.AllowNetworkAccess {
		t.Error("--allow-network-access did not set AllowNetworkAccess")
	}
	for _, v := range []string{"1", "true", "TRUE"} {
		c, err = Load(nil, func(k string) string {
			if k == "REVERB_ALLOW_NETWORK_ACCESS" {
				return v
			}
			return ""
		})
		if err != nil {
			t.Fatal(err)
		}
		if !c.AllowNetworkAccess {
			t.Errorf("REVERB_ALLOW_NETWORK_ACCESS=%q did not set AllowNetworkAccess", v)
		}
	}
}

func TestP2PPortDefaultsFixed(t *testing.T) {
	c, err := Load(nil, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	// A random port would invalidate any peer address the user wrote down.
	if c.P2PPort != DefaultP2PPort {
		t.Errorf("P2PPort = %d, want %d", c.P2PPort, DefaultP2PPort)
	}
	c, err = Load([]string{"--p2p-port", "0"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if c.P2PPort != 0 {
		t.Errorf("P2PPort = %d, want 0", c.P2PPort)
	}
}

func TestAllowedHostsDefaultsEmpty(t *testing.T) {
	c, err := Load(nil, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(c.AllowedHosts) != 0 {
		t.Errorf("AllowedHosts = %v, want empty; loopback is allowed without configuration", c.AllowedHosts)
	}
}

func TestAllowedHostsFlagAndEnv(t *testing.T) {
	c, err := Load([]string{"--allowed-hosts", "music.example.com, reverb.local "}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(c.AllowedHosts) != 2 || c.AllowedHosts[0] != "music.example.com" || c.AllowedHosts[1] != "reverb.local" {
		t.Errorf("AllowedHosts = %v, want the two hosts trimmed", c.AllowedHosts)
	}

	c, err = Load(nil, func(k string) string {
		if k == "REVERB_ALLOWED_HOSTS" {
			return "a.example,b.example"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.AllowedHosts) != 2 {
		t.Errorf("REVERB_ALLOWED_HOSTS = %v, want 2 hosts", c.AllowedHosts)
	}
}

func TestAllowedHostsFlagWinsOverEnv(t *testing.T) {
	c, err := Load([]string{"--allowed-hosts", "flag.example"}, func(k string) string {
		if k == "REVERB_ALLOWED_HOSTS" {
			return "env.example"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.AllowedHosts) != 1 || c.AllowedHosts[0] != "flag.example" {
		t.Errorf("AllowedHosts = %v, want [flag.example]", c.AllowedHosts)
	}
}
