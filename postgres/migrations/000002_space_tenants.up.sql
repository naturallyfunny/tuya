CREATE TABLE IF NOT EXISTS "tuya_space_tenants" (
    "owner"         text                     PRIMARY KEY,
    "root_space_id" bigint                   NOT NULL CHECK ("root_space_id" <> 0),
    "created_at"    timestamp with time zone DEFAULT NOW(),
    "updated_at"    timestamp with time zone DEFAULT NOW(),
    "deleted_at"    timestamp with time zone
);
