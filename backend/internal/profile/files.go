package profile

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var storageKeyPattern = regexp.MustCompile(`^ja-[a-f0-9]{32}\.(pdf|docx)$`)

// Vault confines private file access to a non-public directory using an OS filesystem root.
type Vault struct{ root *os.Root }

func OpenVault(directory string) (*Vault, error) {
	if directory == "" {
		return nil, fmt.Errorf("private resume directory required")
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &Vault{root: root}, nil
}
func (v *Vault) Close() error { return v.root.Close() }

func (v *Vault) put(filename string, data []byte) (string, string, error) {
	if err := ValidateFile(filename, data); err != nil {
		return "", "", err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	key := "ja-" + hex.EncodeToString(raw[:]) + strings.ToLower(filepath.Ext(filename))
	sum := sha256.Sum256(data)
	file, err := v.root.OpenFile(key, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", "", err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		removeErr := v.root.Remove(key)
		return "", "", errors.Join(errors.New("private resume write failed"), writeErr, closeErr, removeErr)
	}
	return key, hex.EncodeToString(sum[:]), nil
}

func (v *Vault) read(key string) ([]byte, error) {
	if !storageKeyPattern.MatchString(key) {
		return nil, ErrInvalidFile
	}
	file, err := v.root.Open(key)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, ErrInvalidFile
	}
	return data, nil
}

func (v *Vault) remove(key string) error {
	if !storageKeyPattern.MatchString(key) {
		return ErrInvalidFile
	}
	err := v.root.Remove(key)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
