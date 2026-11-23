// Package localstate provides atomic writes for private files.
package localstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type Store struct {
	Dir  string
	lock *os.File
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "controller.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another controller is using this state directory")
	}
	return &Store{Dir: dir, lock: f}, nil
}
func (s *Store) Close() error { return s.lock.Close() }
func (s *Store) Read(name string, out any) error {
	b, err := os.ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		return err
	}
	if json.Unmarshal(b, out) != nil {
		return fmt.Errorf("invalid state file %s", name)
	}
	return nil
}
func (s *Store) Write(name string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return WritePrivate(filepath.Join(s.Dir, name), append(b, '\n'))
}
func WritePrivate(filename string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(filename), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filename); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(filename))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
