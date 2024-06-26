package grip

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

type RepoEntry struct {
	Name        string
	Tag         string
	Repo        string
	InstallPath string
}

func writeLockFile(entries []RepoEntry) error {
	file, err := os.Create(lockFilepath)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, entry := range entries {
		_, err := file.WriteString(fmt.Sprintf("%s %s %s %s\n", entry.Name, entry.Tag, entry.Repo, entry.InstallPath))
		if err != nil {
			return err
		}
	}
	return nil
}

func GetLockFileLines() ([][]string, error) {
	file, err := os.Open(lockFilepath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries [][]string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) >= 4 {
			entries = append(entries, parts)
		}
	}

	return entries, scanner.Err()
}

func GetAllEntries() ([]RepoEntry, error) {
	lines, err := GetLockFileLines()
	if err != nil {
		return nil, err
	}

	var entries []RepoEntry
	for _, parts := range lines {
		if len(parts) >= 4 {
			entry := RepoEntry{
				Name:        parts[0],
				Tag:         parts[1],
				Repo:        parts[2],
				InstallPath: parts[3],
			}
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func GetEntryByRepo(repo string) (*RepoEntry, error) {
	entries, err := GetAllEntries()
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.Repo == repo {
			return &entry, nil
		}
	}

	return nil, errors.New("no entry found")
}

func GetEntryByName(name string) (*RepoEntry, error) {
	entries, err := GetAllEntries()
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.Name == name {
			return &entry, nil
		}
	}

	return nil, errors.New("no entry found")
}

func UpdateEntry(updatedEntry RepoEntry) error {
	entries, err := GetAllEntries()
	if err != nil {
		return err
	}

	// Find and update the entry
	updated := false
	for i, entry := range entries {
		if entry.Repo == updatedEntry.Repo && entry.Name == updatedEntry.Name {
			entries[i] = updatedEntry
			updated = true
			break
		}
	}

	if !updated {
		// If the entry was not found, add it
		entries = append(entries, updatedEntry)
	}

	return writeLockFile(entries)
}

func AddEntry(updatedEntry RepoEntry) error {
	entries, err := GetAllEntries()
	if err != nil {
		return err
	}

	// Find and update the entry
	found := false
	for _, entry := range entries {
		if entry.Name == updatedEntry.Name {
			found = true
			break
		}
	}

	if found {
		return fmt.Errorf("entry already exists")
	}

	entries = append(entries, updatedEntry)
	return writeLockFile(entries)
}

func DeleteEntryByRepo(repo string) error {
	entries, err := GetAllEntries()
	if err != nil {
		return err
	}

	var newEntries []RepoEntry
	for _, entry := range entries {
		if entry.Repo != repo {
			newEntries = append(newEntries, entry)
		}
	}

	return writeLockFile(newEntries)
}

func DeleteEntryByName(name string) error {
	entries, err := GetAllEntries()
	if err != nil {
		return err
	}

	var newEntries []RepoEntry
	for _, entry := range entries {
		if entry.Name != name {
			newEntries = append(newEntries, entry)
		}
	}

	return writeLockFile(newEntries)
}
