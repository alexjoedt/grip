package grip

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Installation represents an installed package
type Installation struct {
	Name        string    `json:"name"`
	Alias       string    `json:"alias,omitempty"`
	Repo        string    `json:"repo"`
	Tag         string    `json:"tag"`
	SHA256      string    `json:"sha256,omitempty"`
	InstalledAt time.Time `json:"installedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	InstallPath string    `json:"installPath"`
}

// repoEntry is used for migrating from the old lock file format
type repoEntry struct {
	Name        string
	Tag         string
	Repo        string
	InstallPath string
}

// Storage manages installed packages
type Storage struct {
	filepath string
	mu       sync.RWMutex
}

// NewStorage creates a new storage instance with migration from old lock file
func NewStorage(filepath string, cfg *Config) (*Storage, error) {
	s := &Storage{filepath: filepath}

	// Check if new storage exists
	if _, err := os.Stat(filepath); os.IsNotExist(err) {
		// Try to migrate from old lock file
		oldLockPath := cfg.HomeDir + "/grip.lock"
		if _, err := os.Stat(oldLockPath); err == nil {
			if err := s.migrateFromLockFile(oldLockPath); err != nil {
				// If migration fails, just create empty storage
				if err := s.save(make(map[string]*Installation)); err != nil {
					return nil, fmt.Errorf("initialize storage: %w", err)
				}
			}
		} else {
			// No old file, create empty
			if err := s.save(make(map[string]*Installation)); err != nil {
				return nil, fmt.Errorf("initialize storage: %w", err)
			}
		}
	}

	return s, nil
}

// Get retrieves installation by name
func (s *Storage) Get(name string) (*Installation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	inst, ok := data[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	return inst, nil
}

// GetByRepo retrieves installation by repository path
func (s *Storage) GetByRepo(repo string) (*Installation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	for _, inst := range data {
		if inst.Repo == repo {
			return inst, nil
		}
	}

	return nil, fmt.Errorf("%w: repo %s", ErrNotFound, repo)
}

// List returns all installations
func (s *Storage) List() ([]*Installation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := s.load()
	if err != nil {
		return nil, err
	}

	result := make([]*Installation, 0, len(data))
	for _, inst := range data {
		result = append(result, inst)
	}

	return result, nil
}

// Save stores or updates an installation
func (s *Storage) Save(inst *Installation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	data[inst.Name] = inst
	return s.save(data)
}

// Delete removes an installation by name
func (s *Storage) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.load()
	if err != nil {
		return err
	}

	if _, ok := data[name]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	delete(data, name)
	return s.save(data)
}

// load reads storage from disk
func (s *Storage) load() (map[string]*Installation, error) {
	f, err := os.Open(s.filepath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var data map[string]*Installation
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return nil, err
	}

	return data, nil
}

// save writes storage to disk atomically
func (s *Storage) save(data map[string]*Installation) error {
	tmpPath := s.filepath + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		f.Close()
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.filepath)
}

// migrateFromLockFile imports old text-based lock file
func (s *Storage) migrateFromLockFile(oldPath string) error {
	entries, err := parseOldLockFile(oldPath)
	if err != nil {
		return fmt.Errorf("parse old lock file: %w", err)
	}

	data := make(map[string]*Installation)
	for _, e := range entries {
		inst := &Installation{
			Name:        e.Name,
			Repo:        e.Repo,
			Tag:         e.Tag,
			InstallPath: e.InstallPath,
			InstalledAt: time.Now(), // Unknown, use current time
			UpdatedAt:   time.Now(),
		}

		// Calculate hash of existing binary if it exists
		binPath := e.InstallPath + "/" + e.Name
		if hash, err := calculateFileSHA256(binPath); err == nil {
			inst.SHA256 = hash
		}

		data[e.Name] = inst
	}

	if err := s.save(data); err != nil {
		return err
	}

	// Backup old lock file
	if err := os.Rename(oldPath, oldPath+".backup"); err != nil {
		// Non-fatal, just log
		fmt.Printf("Warning: could not backup old lock file: %v\n", err)
	}

	return nil
}

// parseOldLockFile reads the old text-based format
func parseOldLockFile(path string) ([]repoEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []repoEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) >= 4 {
			entry := repoEntry{
				Name:        parts[0],
				Tag:         parts[1],
				Repo:        parts[2],
				InstallPath: parts[3],
			}
			entries = append(entries, entry)
		}
	}

	return entries, scanner.Err()
}

// CalculateFileSHA256 computes the SHA256 hash of a file (exported for use in commands)
func CalculateFileSHA256(path string) (string, error) {
	return calculateFileSHA256(path)
}

// calculateFileSHA256 computes the SHA256 hash of a file
func calculateFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
