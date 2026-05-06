package mcpbridge

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type TokenStore struct {
	path string
	mu   sync.RWMutex
}

type TokenRecord struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

type TokenSummary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Masked    string    `json:"masked"`
	CreatedAt time.Time `json:"created_at"`
}

type CreatedToken struct {
	TokenSummary
	Token string `json:"token"`
}

func NewTokenStore(dir string) *TokenStore {
	return &TokenStore{path: filepath.Join(dir, "mcp_tokens.json")}
}

func (s *TokenStore) List() ([]TokenSummary, error) {
	records, err := s.load()
	if err != nil {
		return nil, err
	}

	items := make([]TokenSummary, 0, len(records))
	for _, record := range records {
		items = append(items, summarize(record))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *TokenStore) Create(name, token string) (CreatedToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "MCP Token"
	}
	token = strings.TrimSpace(token)
	if token == "" {
		generated, err := generateToken()
		if err != nil {
			return CreatedToken{}, err
		}
		token = generated
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.loadLocked()
	if err != nil {
		return CreatedToken{}, err
	}
	for _, record := range records {
		if constantTokenEqual(record.Token, token) {
			return CreatedToken{}, fmt.Errorf("mcp token already exists")
		}
	}

	record := TokenRecord{
		ID:        tokenID(token),
		Name:      name,
		Token:     token,
		CreatedAt: time.Now().UTC(),
	}
	records = append(records, record)
	if err := s.saveLocked(records); err != nil {
		return CreatedToken{}, err
	}

	return CreatedToken{TokenSummary: summarize(record), Token: token}, nil
}

func (s *TokenStore) Delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("token id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.loadLocked()
	if err != nil {
		return err
	}
	next := records[:0]
	deleted := false
	for _, record := range records {
		if record.ID == id {
			deleted = true
			continue
		}
		next = append(next, record)
	}
	if !deleted {
		return fmt.Errorf("mcp token not found")
	}
	return s.saveLocked(next)
}

func (s *TokenStore) Verify(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	records, err := s.load()
	if err != nil {
		return false
	}
	for _, record := range records {
		if constantTokenEqual(record.Token, token) {
			return true
		}
	}
	return false
}

func (s *TokenStore) load() ([]TokenRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked()
}

func (s *TokenStore) loadLocked() ([]TokenRecord, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []TokenRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *TokenStore) saveLocked(records []TokenRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

func summarize(record TokenRecord) TokenSummary {
	return TokenSummary{
		ID:        record.ID,
		Name:      record.Name,
		Masked:    MaskToken(record.Token),
		CreatedAt: record.CreatedAt,
	}
}

func MaskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return token[:1] + "..." + token[len(token)-1:]
	}
	return token[:4] + "..." + token[len(token)-4:]
}

func generateToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "cfui_mcp_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func tokenID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}

func constantTokenEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
