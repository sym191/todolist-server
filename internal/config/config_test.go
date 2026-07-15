package config

import "testing"

func TestLoadRequiresDatabaseAndStrongSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "short")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected validation error")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.SyncBatchSize != 200 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}
