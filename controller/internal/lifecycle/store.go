package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type FileStore struct{ Path string }

func (s FileStore) Load() (Intent, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Intent{Version: 1, Mode: "manual", ManualOff: true}, nil
	}
	if err != nil {
		return Intent{}, err
	}
	var v Intent
	if err = json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("intent file is invalid; refusing automatic start: %w", err)
	}
	if v.Version != 1 || (v.Mode != "manual" && v.Mode != "follow-party") {
		return v, errors.New("unsupported controller intent; preserve the file and repair it before starting")
	}
	return v, nil
}
func (s FileStore) Save(v Intent) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(s.Path, append(b, '\n'))
}
func AtomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".intent-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
