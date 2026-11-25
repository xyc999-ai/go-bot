package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"go-bot/harness/session"
	"go-bot/internal/localstate"
)

func chatMetadataPath(store sessionformat.FileStore, id, name string) (string, error) {
	logPath, err := store.Path(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(logPath), name), nil
}

func archivedChat(store sessionformat.FileStore, id string) (bool, error) {
	marker, err := chatMetadataPath(store, id, "archived")
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func archiveChat(ctx context.Context, store sessionformat.FileStore, id string) error {
	if _, err := store.ReadHeader(ctx, id); err != nil {
		return err
	}
	marker, err := chatMetadataPath(store, id, "archived")
	if err != nil {
		return err
	}
	return localstate.WritePrivate(marker, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"))
}
