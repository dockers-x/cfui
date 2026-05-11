package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// MCPToken stores one MCP access token.
type MCPToken struct {
	ent.Schema
}

// Fields of the MCPToken.
func (MCPToken) Fields() []ent.Field {
	return []ent.Field{
		field.String("token_id").NotEmpty().Unique(),
		field.String("name").NotEmpty(),
		field.String("token_hash").NotEmpty().Unique(),
		field.String("masked").NotEmpty(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
