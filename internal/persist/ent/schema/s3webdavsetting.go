package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// S3WebDAVSetting stores one S3 WebDAV mount configuration.
type S3WebDAVSetting struct {
	ent.Schema
}

// Annotations of the S3WebDAVSetting.
func (S3WebDAVSetting) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "s3_webdav_settings"},
	}
}

// Fields of the S3WebDAVSetting.
func (S3WebDAVSetting) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").NotEmpty().Unique(),
		field.String("name").Default("Default S3"),
		field.Int("sort_order").Default(0),
		field.Bool("enabled").Default(true),
		field.Bool("webdav_enabled").Default(true),
		field.Bool("webdav_auth_enabled").Default(true),
		field.String("provider").Default("generic_s3"),
		field.String("endpoint_url").Default(""),
		field.String("region").Default("auto"),
		field.Bool("path_style").Default(true),
		field.String("account_id").Default(""),
		field.String("bucket_name").Default(""),
		field.String("root_prefix").Default(""),
		field.String("mount_path").Default("/webdav/s3/"),
		field.String("jurisdiction").Default("default"),
		field.String("access_key_id").Default("").Sensitive(),
		field.String("secret_access_key").Default("").Sensitive(),
		field.String("webdav_username").Default(""),
		field.String("webdav_password_hash").Default("").Sensitive(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
