CREATE TABLE IF NOT EXISTS "tuya_spaces" (
    "owner"      text                     PRIMARY KEY,
    "space_id"   bigint                   NOT NULL CHECK ("space_id" <> 0),
    "created_at" timestamp with time zone DEFAULT NOW(),
    "updated_at" timestamp with time zone DEFAULT NOW(),
    "deleted_at" timestamp with time zone
);
