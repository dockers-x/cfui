package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// TunnelProfile stores one Cloudflare Tunnel profile.
type TunnelProfile struct {
	ent.Schema
}

// Annotations of the TunnelProfile.
func (TunnelProfile) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "tunnel_profiles"},
	}
}

// Fields of the TunnelProfile.
func (TunnelProfile) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").NotEmpty().Unique(),
		field.String("name").Default("Tunnel 1"),
		field.Int("sort_order").Default(0),
		field.String("token").Default("").Sensitive(),
		field.Bool("local_enabled").Default(true),
		field.Bool("remote_management_enabled").Default(false),
		field.String("account_id").Default(""),
		field.String("tunnel_id").Default(""),
		field.Bool("auto_start").Default(false),
		field.Bool("auto_restart").Default(true),
		field.String("custom_tag").Default(""),
		field.String("software_name").Default("cfui"),
		field.String("protocol").Default("auto"),
		field.String("grace_period").Default("30s"),
		field.String("region").Default(""),
		field.Int("retries").Default(5),
		field.Bool("metrics_enable").Default(false),
		field.Int("metrics_port").Default(60123),
		field.String("log_level").Default("info"),
		field.String("log_file").Default(""),
		field.Bool("log_json").Default(false),
		field.String("edge_ip_version").Default("auto"),
		field.String("edge_bind_address").Default(""),
		field.String("edge").Default(""),
		field.Bool("post_quantum").Default(false),
		field.Bool("no_tls_verify").Default(false),
		field.String("extra_args").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
