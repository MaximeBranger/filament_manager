CREATE TABLE shares (
  id               INTEGER PRIMARY KEY,
  user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash       TEXT NOT NULL UNIQUE,
  title            TEXT NOT NULL,
  password_hash    TEXT,
  password_version INTEGER NOT NULL DEFAULT 0,
  failed_attempts  INTEGER NOT NULL DEFAULT 0,
  locked_at        TEXT,
  created_at       TEXT NOT NULL,
  expires_at       TEXT NOT NULL
);
CREATE INDEX shares_user ON shares(user_id);
CREATE INDEX shares_expires ON shares(expires_at);

CREATE TABLE share_files (
  id             INTEGER PRIMARY KEY,
  share_id       INTEGER NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
  original_name  TEXT NOT NULL,
  storage_key    TEXT NOT NULL UNIQUE,
  triangles      INTEGER NOT NULL,
  size_x_mm      REAL NOT NULL,
  size_y_mm      REAL NOT NULL,
  size_z_mm      REAL NOT NULL,
  bytes          INTEGER NOT NULL,
  created_at     TEXT NOT NULL
);
CREATE INDEX share_files_share ON share_files(share_id);

CREATE TABLE share_choices (
  share_id        INTEGER PRIMARY KEY REFERENCES shares(id) ON DELETE CASCADE,
  product_id      INTEGER REFERENCES products(id) ON DELETE SET NULL,
  label           TEXT NOT NULL,
  color_hex       TEXT NOT NULL,
  recipient_name  TEXT,
  message         TEXT,
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);
