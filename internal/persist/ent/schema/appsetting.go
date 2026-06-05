package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// AppSetting stores singleton non-secret application settings.
type AppSetting struct {
	ent.Schema
}

// Fields of the AppSetting.
func (AppSetting) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").NotEmpty().Unique(),
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
		field.Bool("post_quantum").Default(false),
		field.Bool("no_tls_verify").Default(false),
		field.String("extra_args").Default(""),
		field.Bool("mcp_enabled").Default(false),
		field.Bool("s3_webdav_enabled").Default(false),
		field.String("s3_webdav_active_key").Default("default"),
		field.String("s3_webdav_access_mode").Default("main"),
		field.String("s3_webdav_dedicated_bind_host").Default(""),
		field.Int("s3_webdav_dedicated_port").Default(14334),
		field.Bool("s3_webdav_dedicated_auto_start").Default(false),
		field.String("s3_webdav_dedicated_domain_mode").Default("none"),
		field.String("s3_webdav_dedicated_custom_domain").Default(""),
		field.String("s3_webdav_dedicated_tunnel_hostname").Default(""),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
