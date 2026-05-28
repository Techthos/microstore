package db

import (
	"encoding/json"
	"errors"
	"fmt"

	bolt "go.etcd.io/bbolt"
	"techthos.net/microstore/internal/models"
)

// ErrNotFound is returned by repository lookups/deletes when no record exists
// for the given key. Match it with errors.Is, never on message text.
var ErrNotFound = errors.New("not found")

// InstallRepo persists tracked installs, keyed by repo slug ("owner/name").
// Slugs are stored as their raw bytes; byte order equals alphabetical order, so
// List walks them in listing order.
type InstallRepo struct {
	db *bolt.DB
}

// Get returns the install recorded for repo, or ErrNotFound if none exists.
func (r *InstallRepo) Get(repo string) (*models.InstalledApp, error) {
	if repo == "" {
		return nil, fmt.Errorf("repo slug is empty")
	}
	var app *models.InstalledApp
	err := r.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(installsBucket).Get([]byte(repo))
		if v == nil {
			return ErrNotFound
		}
		var a models.InstalledApp
		if err := json.Unmarshal(v, &a); err != nil {
			return fmt.Errorf("unmarshal install %q: %w", repo, err)
		}
		app = &a
		return nil
	})
	if err != nil {
		return nil, err
	}
	return app, nil
}

// List returns every tracked install in alphabetical (byte) order by slug.
func (r *InstallRepo) List() ([]models.InstalledApp, error) {
	var out []models.InstalledApp
	err := r.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(installsBucket).ForEach(func(k, v []byte) error {
			if v == nil { // nested bucket — none expected here
				return nil
			}
			var a models.InstalledApp
			if err := json.Unmarshal(v, &a); err != nil {
				return fmt.Errorf("unmarshal install %q: %w", k, err)
			}
			out = append(out, a)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Save writes (creating or overwriting) the install record, keyed by its slug.
func (r *InstallRepo) Save(app models.InstalledApp) error {
	if app.Repo == "" {
		return fmt.Errorf("install has empty repo slug")
	}
	data, err := json.Marshal(app)
	if err != nil {
		return fmt.Errorf("marshal install %q: %w", app.Repo, err)
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(installsBucket).Put([]byte(app.Repo), data)
	})
}

// Delete removes the install record for repo, or returns ErrNotFound if there is
// nothing to remove.
func (r *InstallRepo) Delete(repo string) error {
	if repo == "" {
		return fmt.Errorf("repo slug is empty")
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(installsBucket)
		if b.Get([]byte(repo)) == nil {
			return ErrNotFound
		}
		return b.Delete([]byte(repo))
	})
}
