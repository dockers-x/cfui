package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// OAuthState stores short-lived OAuth PKCE state until Cloudflare redirects
// back to cfui.
type OAuthState struct {
	ent.Schema
}

// Fields of the OAuthState.
func (OAuthState) Fields() []ent.Field {
	return []ent.Field{
		field.String("state").NotEmpty().Unique(),
		field.String("code_verifier").NotEmpty().Sensitive(),
		field.String("redirect_uri").NotEmpty(),
		field.String("scope").Default(""),
		field.Time("expires_at"),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
