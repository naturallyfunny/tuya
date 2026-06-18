CREATE TABLE IF NOT EXISTS "tuya_app_accounts" (
    "owner_id"   text                     PRIMARY KEY,
    "tuya_uid"   text                     NOT NULL,
    "created_at" timestamp with time zone DEFAULT NOW(),
    "updated_at" timestamp with time zone DEFAULT NOW(),
    "deleted_at" timestamp with time zone
);
