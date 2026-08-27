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
