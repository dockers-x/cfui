package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OAuthValidationReport stores a sanitized Cloudflare OAuth validation report
// snapshot for comparing real-account scope/API behavior over time.
type OAuthValidationReport struct {
	ent.Schema
}

// Indexes supports fetching the most recently saved reports without sorting the archive.
func (OAuthValidationReport) Indexes() []ent.Index {
	return []ent.Index{index.Fields("saved_at")}
}

// Fields of the OAuthValidationReport.
func (OAuthValidationReport) Fields() []ent.Field {
	return []ent.Field{
		field.String("report_id").NotEmpty().Unique(),
		field.String("session_id").Default(""),
		field.String("session_label").Default(""),
		field.String("account_id").Default(""),
		field.String("account_name").Default(""),
		field.String("zone_id").Default(""),
		field.String("zone_name").Default(""),
		field.Time("generated_at"),
		field.Time("saved_at").Default(time.Now).Immutable(),
		field.Int("scope_missing").Default(0),
		field.Int("api_unavailable").Default(0),
		field.Int("api_missing_scope").Default(0),
		field.Int("action_items").Default(0),
		field.Text("report_body").NotEmpty(),
	}
}
