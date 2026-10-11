package wa

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type Credentials struct {
	NoiseKey    []byte `json:"noise_key"`
	NoiseKeyPub []byte `json:"noise_key_pub"`
	IdentityKey []byte `json:"identity_key"`
	AdvSecretKey []byte `json:"adv_secret_key"`
	WGKey       []byte `json:"wg_key"`
	CurBlob     []byte `json:"cur_blob"`
	RegistrationID uint32 `json:"registration_id"`
	WaID        string `json:"wa_id"`
}

func LoadCredentials(dir string) (*Credentials, error) {
	path := filepath.Join(dir, "creds.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("не удалось прочитать creds.json: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		return nil, fmt.Errorf("битый creds.json: %w", err)
	}
	if len(creds.NoiseKey) != 32 || len(creds.IdentityKey) != 32 {
		return nil, errors.New("некорректные ключи в creds.json")
	}
	return &creds, nil
}

func SaveCredentials(dir string, creds *Credentials) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("не удалось создать папку %s: %w", dir, err)
	}
	raw, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("не удалось сериализовать creds: %w", err)
	}
	tmpPath := filepath.Join(dir, "creds.json.tmp")
	finalPath := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return fmt.Errorf("не удалось записать creds.json: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("не удалось обновить creds.json: %w", err)
	}
	log.Printf("[auth] сохранены учётные данные для %s", creds.WaID)
	return nil
}
