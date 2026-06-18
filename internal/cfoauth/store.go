package cfoauth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"cfui/internal/persist"
	"cfui/internal/persist/ent"
	"cfui/internal/persist/ent/oauthsession"
	"cfui/internal/persist/ent/oauthstate"
)

var (
	ErrNotLoggedIn  = errors.New("cloudflare oauth session is not logged in")
	ErrStateExpired = errors.New("oauth state expired")
)

type Store struct {
	dir      string
	client   *ent.Client
	initOnce sync.Once
	initErr  error
	mu       sync.Mutex
}

type Session struct {
	ID           string
	Label        string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
	Current      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SessionSummary struct {
	ID           string           `json:"id"`
	Label        string           `json:"label"`
	ExpiresAt    time.Time        `json:"expires_at"`
	Scopes       []string         `json:"scopes"`
	Current      bool             `json:"current"`
	Capabilities CapabilityMatrix `json:"capabilities"`
}

type PendingState struct {
	State        string
	CodeVerifier string
	RedirectURI  string
	Scope        string
	ExpiresAt    time.Time
}

func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) ListSessions(ctx context.Context) ([]SessionSummary, error) {
	if err := s.ensureClient(); err != nil {
		return nil, err
	}
	rows, err := s.client.OAuthSession.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]SessionSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, summarize(rowToSession(row)))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Current != items[j].Current {
			return items[i].Current
		}
		return items[i].Label < items[j].Label
	})
	return items, nil
}

func (s *Store) CurrentSession(ctx context.Context) (Session, error) {
	if err := s.ensureClient(); err != nil {
		return Session{}, err
	}
	row, err := s.client.OAuthSession.Query().Where(oauthsession.Current(true)).Only(ctx)
	if ent.IsNotFound(err) {
		return Session{}, ErrNotLoggedIn
	}
	if err != nil {
		return Session{}, err
	}
	return rowToSession(row), nil
}

func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	if err := s.ensureClient(); err != nil {
		return Session{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Session{}, ErrNotLoggedIn
	}
	row, err := s.client.OAuthSession.Query().Where(oauthsession.SessionID(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return Session{}, ErrNotLoggedIn
	}
	if err != nil {
		return Session{}, err
	}
	return rowToSession(row), nil
}

func (s *Store) SaveSession(ctx context.Context, session Session) error {
	if err := s.ensureClient(); err != nil {
		return err
	}
	session.ID = strings.TrimSpace(session.ID)
	session.AccessToken = strings.TrimSpace(session.AccessToken)
	if session.ID == "" || session.AccessToken == "" {
		return fmt.Errorf("session id and access token are required")
	}
	if strings.TrimSpace(session.Label) == "" {
		session.Label = "Cloudflare Account"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.OAuthSession.Update().SetCurrent(false).Save(ctx); err != nil {
		return err
	}

	existing, queryErr := tx.OAuthSession.Query().Where(oauthsession.SessionID(session.ID)).Only(ctx)
	if queryErr == nil {
		_, err = tx.OAuthSession.UpdateOneID(existing.ID).
			SetLabel(session.Label).
			SetAccessToken(session.AccessToken).
			SetRefreshToken(session.RefreshToken).
			SetExpiresAt(session.ExpiresAt).
			SetScope(session.Scope).
			SetCurrent(true).
			Save(ctx)
	} else if ent.IsNotFound(queryErr) {
		_, err = tx.OAuthSession.Create().
			SetSessionID(session.ID).
			SetLabel(session.Label).
			SetAccessToken(session.AccessToken).
			SetRefreshToken(session.RefreshToken).
			SetExpiresAt(session.ExpiresAt).
			SetScope(session.Scope).
			SetCurrent(true).
			Save(ctx)
	} else {
		err = queryErr
	}
	if err != nil {
		return err
	}
	err = tx.Commit()
	return err
}

func (s *Store) UpdateToken(ctx context.Context, session Session) error {
	if err := s.ensureClient(); err != nil {
		return err
	}
	_, err := s.client.OAuthSession.Update().
		Where(oauthsession.SessionID(session.ID)).
		SetAccessToken(session.AccessToken).
		SetRefreshToken(session.RefreshToken).
		SetExpiresAt(session.ExpiresAt).
		SetScope(session.Scope).
		Save(ctx)
	return err
}

func (s *Store) UpdateSessionLabel(ctx context.Context, id, label string) error {
	if err := s.ensureClient(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	label = strings.TrimSpace(label)
	if id == "" {
		return ErrNotLoggedIn
	}
	count, err := s.client.OAuthSession.Update().
		Where(oauthsession.SessionID(id)).
		SetLabel(label).
		Save(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotLoggedIn
	}
	return nil
}

func (s *Store) SwitchSession(ctx context.Context, id string) error {
	if err := s.ensureClient(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrNotLoggedIn
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	target, queryErr := tx.OAuthSession.Query().Where(oauthsession.SessionID(id)).Only(ctx)
	if ent.IsNotFound(queryErr) {
		err = ErrNotLoggedIn
		return err
	}
	if queryErr != nil {
		err = queryErr
		return err
	}
	if _, err = tx.OAuthSession.Update().SetCurrent(false).Save(ctx); err != nil {
		return err
	}
	if _, err = tx.OAuthSession.UpdateOneID(target.ID).SetCurrent(true).Save(ctx); err != nil {
		return err
	}
	err = tx.Commit()
	return err
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if err := s.ensureClient(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		current, err := s.CurrentSession(ctx)
		if err != nil {
			return err
		}
		id = current.ID
	}
	count, err := s.client.OAuthSession.Delete().Where(oauthsession.SessionID(id)).Exec(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotLoggedIn
	}
	return s.ensureCurrentSession(ctx)
}

func (s *Store) SaveState(ctx context.Context, state PendingState) error {
	if err := s.ensureClient(); err != nil {
		return err
	}
	state.State = strings.TrimSpace(state.State)
	state.CodeVerifier = strings.TrimSpace(state.CodeVerifier)
	state.RedirectURI = strings.TrimSpace(state.RedirectURI)
	if state.State == "" || state.CodeVerifier == "" || state.RedirectURI == "" {
		return fmt.Errorf("oauth state, verifier, and redirect uri are required")
	}
	_, _ = s.client.OAuthState.Delete().Where(oauthstate.ExpiresAtLT(time.Now().UTC())).Exec(ctx)
	_, err := s.client.OAuthState.Create().
		SetState(state.State).
		SetCodeVerifier(state.CodeVerifier).
		SetRedirectURI(state.RedirectURI).
		SetScope(state.Scope).
		SetExpiresAt(state.ExpiresAt).
		Save(ctx)
	return err
}

func (s *Store) ConsumeState(ctx context.Context, state string) (PendingState, error) {
	if err := s.ensureClient(); err != nil {
		return PendingState{}, err
	}
	state = strings.TrimSpace(state)
	if state == "" {
		return PendingState{}, fmt.Errorf("oauth state is required")
	}
	row, err := s.client.OAuthState.Query().Where(oauthstate.State(state)).Only(ctx)
	if ent.IsNotFound(err) {
		return PendingState{}, ErrStateExpired
	}
	if err != nil {
		return PendingState{}, err
	}
	_ = s.client.OAuthState.DeleteOneID(row.ID).Exec(ctx)
	if time.Now().UTC().After(row.ExpiresAt) {
		return PendingState{}, ErrStateExpired
	}
	return PendingState{
		State:        row.State,
		CodeVerifier: row.CodeVerifier,
		RedirectURI:  row.RedirectURI,
		Scope:        row.Scope,
		ExpiresAt:    row.ExpiresAt,
	}, nil
}

func (s *Store) ensureClient() error {
	s.initOnce.Do(func() {
		client, err := persist.OpenClient(s.dir)
		if err != nil {
			s.initErr = err
			return
		}
		s.client = client
	})
	return s.initErr
}

func (s *Store) ensureCurrentSession(ctx context.Context) error {
	if _, err := s.CurrentSession(ctx); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotLoggedIn) {
		return err
	}
	first, err := s.client.OAuthSession.Query().Order(ent.Asc(oauthsession.FieldCreatedAt)).First(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.client.OAuthSession.UpdateOneID(first.ID).SetCurrent(true).Save(ctx)
	return err
}

func rowToSession(row *ent.OAuthSession) Session {
	return Session{
		ID:           row.SessionID,
		Label:        row.Label,
		AccessToken:  row.AccessToken,
		RefreshToken: row.RefreshToken,
		ExpiresAt:    row.ExpiresAt,
		Scope:        row.Scope,
		Current:      row.Current,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
}

func summarize(session Session) SessionSummary {
	scopes := strings.Fields(session.Scope)
	sort.Strings(scopes)
	return SessionSummary{
		ID:           session.ID,
		Label:        session.Label,
		ExpiresAt:    session.ExpiresAt,
		Scopes:       scopes,
		Current:      session.Current,
		Capabilities: Capabilities(session.Scope),
	}
}
