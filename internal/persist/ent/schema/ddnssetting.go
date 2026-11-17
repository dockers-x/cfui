package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// DDNSSetting stores singleton DDNS behavior settings.
type DDNSSetting struct {
	ent.Schema
}

// Fields of the DDNSSetting.
func (DDNSSetting) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").NotEmpty().Unique(),
		field.Bool("enabled").Default(false),
		field.Int("interval_mins").Default(5),
		field.Bool("only_on_change").Default(true),
		field.Int("max_retries").Default(3),
		field.Bool("use_proxy").Default(false),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
