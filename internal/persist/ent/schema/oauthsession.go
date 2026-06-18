package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// OAuthSession stores one Cloudflare OAuth identity and its token set.
type OAuthSession struct {
	ent.Schema
}

// Fields of the OAuthSession.
func (OAuthSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("session_id").NotEmpty().Unique(),
		field.String("label").Default(""),
		field.String("access_token").NotEmpty().Sensitive(),
		field.String("refresh_token").Default("").Sensitive(),
		field.Time("expires_at"),
		field.String("scope").Default(""),
		field.Bool("current").Default(false),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
