package config

import (
	"testing"
	"time"
)

func TestLoadBytesAppliesDefaultsAndOverrides(t *testing.T) {
	t.Setenv("GABLE_HTTP_ADDR", "127.0.0.1:9000")
	cfg, err := LoadBytes([]byte(`
app:
  name: demo
log:
  level: debug
http:
  read_timeout: 250ms
db:
  driver: mysql
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.Name != "demo" || cfg.App.Env != "development" {
		t.Fatalf("app = %+v", cfg.App)
	}
	if cfg.Log.Dir != "logs" || cfg.Log.Level != "debug" {
		t.Fatalf("log = %+v", cfg.Log)
	}
	if cfg.HTTP.Addr != "127.0.0.1:9000" {
		t.Fatalf("http.addr = %q", cfg.HTTP.Addr)
	}
	if cfg.HTTP.ReadTimeout.Std() != 250*time.Millisecond {
		t.Fatalf("read timeout = %v", cfg.HTTP.ReadTimeout.Std())
	}
	if cfg.HTTP.WriteTimeout.Std() != 10*time.Second {
		t.Fatalf("write timeout default lost: %v", cfg.HTTP.WriteTimeout.Std())
	}
	if cfg.DB.Driver != "mysql" || cfg.DB.MaxOpen != 20 {
		t.Fatalf("db = %+v", cfg.DB)
	}
}

func TestLoadBytesExpandsEnvironment(t *testing.T) {
	t.Setenv("DEMO_DSN", "user:pass@tcp(localhost:3306)/demo")
	cfg, err := LoadBytes([]byte("app:\n  name: demo\ndb:\n  dsn: ${DEMO_DSN}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.DSN != "user:pass@tcp(localhost:3306)/demo" {
		t.Fatalf("dsn = %q", cfg.DB.DSN)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := []string{
		"app:\n  name: ''\n",
		"app:\n  name: demo\nlog:\n  level: verbose\n",
		"app:\n  name: demo\nlog:\n  format: xml\n",
		"app:\n  name: demo\nhttp:\n  addr: ''\n",
	}
	for _, raw := range cases {
		if _, err := LoadBytes([]byte(raw)); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}
