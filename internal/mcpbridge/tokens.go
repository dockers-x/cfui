package mcpbridge

import (
	"cfui/internal/logger"
	"cfui/internal/persist"
	"cfui/internal/persist/ent"
	"cfui/internal/persist/ent/mcptoken"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	dir      string
	client   *ent.Client
	initOnce sync.Once
	initErr  error
	mu       sync.RWMutex
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
	return &TokenStore{dir: dir}
}

func (s *TokenStore) List() ([]TokenSummary, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.client.MCPToken.Query().All(context.Background())
	if err != nil {
		return nil, err
	}

	items := make([]TokenSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, TokenSummary{
			ID:        row.TokenID,
			Name:      row.Name,
			Masked:    row.Masked,
			CreatedAt: row.CreatedAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (s *TokenStore) Create(name, token string) (CreatedToken, error) {
	if err := s.ensureClient(); err != nil {
		return CreatedToken{}, err
	}

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

	hash := tokenHash(token)
	if _, err := s.client.MCPToken.Query().Where(mcptoken.TokenHash(hash)).Only(context.Background()); err == nil {
		return CreatedToken{}, fmt.Errorf("mcp token already exists")
	} else if !ent.IsNotFound(err) {
		return CreatedToken{}, err
	}

	record := TokenSummary{
		ID:        tokenID(token),
		Name:      name,
		Masked:    MaskToken(token),
		CreatedAt: time.Now().UTC(),
	}
	if _, err := s.client.MCPToken.Create().
		SetTokenID(record.ID).
		SetName(record.Name).
		SetTokenHash(hash).
		SetMasked(record.Masked).
		SetCreatedAt(record.CreatedAt).
		Save(context.Background()); err != nil {
		return CreatedToken{}, err
	}

	return CreatedToken{TokenSummary: record, Token: token}, nil
}

func (s *TokenStore) Delete(id string) error {
	if err := s.ensureClient(); err != nil {
		return err
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("token id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, err := s.client.MCPToken.Query().Where(mcptoken.TokenID(id)).Only(context.Background())
	if ent.IsNotFound(err) {
		return fmt.Errorf("mcp token not found")
	}
	if err != nil {
		return err
	}

	return s.client.MCPToken.DeleteOneID(record.ID).Exec(context.Background())
}

func (s *TokenStore) Verify(token string) bool {
	if err := s.ensureClient(); err != nil {
		return false
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, err := s.client.MCPToken.Query().Where(mcptoken.TokenHash(tokenHash(token))).Only(context.Background())
	return err == nil
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

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *TokenStore) ensureClient() error {
	s.initOnce.Do(func() {
		client, err := persist.OpenClient(s.dir)
		if err != nil {
			s.initErr = err
			return
		}

		s.client = client
		s.initErr = s.migrateLegacyTokens(context.Background())
	})

	return s.initErr
}

func (s *TokenStore) migrateLegacyTokens(ctx context.Context) error {
	count, err := s.client.MCPToken.Query().Count(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	legacyPath := filepath.Join(s.dir, "mcp_tokens.json")
	records, err := loadLegacyTokenRecords(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	seen := make(map[string]struct{}, len(records))
	builders := make([]*ent.MCPTokenCreate, 0, len(records))
	now := time.Now().UTC()
	for _, record := range records {
		token := strings.TrimSpace(record.Token)
		if token == "" {
			continue
		}

		hash := tokenHash(token)
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}

		name := strings.TrimSpace(record.Name)
		if name == "" {
			name = "MCP Token"
		}

		createdAt := record.CreatedAt.UTC()
		if createdAt.IsZero() {
			createdAt = now
		}

		id := strings.TrimSpace(record.ID)
		if id == "" {
			id = tokenID(token)
		}

		builders = append(builders, s.client.MCPToken.Create().
			SetTokenID(id).
			SetName(name).
			SetTokenHash(hash).
			SetMasked(MaskToken(token)).
			SetCreatedAt(createdAt))
	}

	if len(builders) > 0 {
		if err := s.client.MCPToken.CreateBulk(builders...).Exec(ctx); err != nil {
			return err
		}
	}

	if err := persist.MarkLegacyMigrated(legacyPath); err != nil && !os.IsNotExist(err) {
		if logger.Sugar != nil {
			logger.Sugar.Warnf("Failed to rename migrated legacy token file %s: %v", legacyPath, err)
		}
	} else {
		if logger.Sugar != nil {
			logger.Sugar.Infof("Migrated legacy MCP tokens from %s to %s", legacyPath, persist.DBPath(s.dir))
		}
	}

	return nil
}

func loadLegacyTokenRecords(path string) ([]TokenRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var records []TokenRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}
