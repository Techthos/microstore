package db

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
	"techthos.net/microstore/internal/models"
)

// configKey is the single well-known key (within configBucket) holding the
// Config JSON document.
var configKey = []byte("config")

// ConfigRepo persists the singleton store Config.
type ConfigRepo struct {
	db *bolt.DB
}

// defaultManifestURL is the curated catalog applied on first run. It points at
// the raw catalog.json published from this repository; the user may override it
// from the Config screen or set_config.
const defaultManifestURL = "https://raw.githubusercontent.com/Techthos/microstore/main/catalog.json"

// DefaultConfig is the configuration applied on first run: the default manifest
// URL (the curated catalog published from this repo) and the managed install
// directory under the user's home.
func DefaultConfig() models.Config {
	return models.Config{
		ManifestURL: defaultManifestURL,
		InstallDir:  defaultInstallDir(),
	}
}

func defaultInstallDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".local", "share", "microstore", "bin")
	}
	return filepath.Join(home, ".local", "share", "microstore", "bin")
}

// Load returns the persisted Config, or DefaultConfig when none has been saved.
func (r *ConfigRepo) Load() (models.Config, error) {
	cfg := DefaultConfig()
	err := r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(configBucket)
		if b == nil {
			return nil
		}
		v := b.Get(configKey)
		if v == nil {
			return nil
		}
		var stored models.Config
		if err := json.Unmarshal(v, &stored); err != nil {
			return fmt.Errorf("unmarshal config: %w", err)
		}
		cfg = stored
		return nil
	})
	if err != nil {
		return models.Config{}, err
	}
	return cfg, nil
}

// Save writes the Config document, overwriting any previous value.
func (r *ConfigRepo) Save(cfg models.Config) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(configBucket)
		if b == nil {
			return fmt.Errorf("bucket %q not found", configBucket)
		}
		return b.Put(configKey, data)
	})
}
