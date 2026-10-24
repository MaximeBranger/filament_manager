CREATE TABLE users (
  id                  INTEGER PRIMARY KEY,
  email               TEXT NOT NULL UNIQUE COLLATE NOCASE,
  display_name        TEXT NOT NULL,
  email_notifications INTEGER NOT NULL DEFAULT 1,
  created_at          TEXT NOT NULL
);

CREATE TABLE sessions (
  id_hash      TEXT PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE login_challenges (
  id           INTEGER PRIMARY KEY,
  email        TEXT NOT NULL COLLATE NOCASE,
  token_hash   TEXT NOT NULL UNIQUE,
  code_hash    TEXT NOT NULL,
  browser_hash TEXT NOT NULL,
  attempts     INTEGER NOT NULL DEFAULT 0,
  next_path    TEXT,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  used_at      TEXT
);
CREATE INDEX login_challenges_browser ON login_challenges(browser_hash);

CREATE TABLE products (
  id               INTEGER PRIMARY KEY,
  user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  brand            TEXT NOT NULL,
  brand_norm       TEXT NOT NULL,
  material         TEXT NOT NULL,
  material_other   TEXT,
  color_name       TEXT NOT NULL,
  color_family     TEXT NOT NULL,
  color_hex        TEXT,
  finish           TEXT NOT NULL DEFAULT 'standard',
  finish_other     TEXT,
  diameter_mm      REAL NOT NULL DEFAULT 1.75,
  nominal_weight_g INTEGER NOT NULL DEFAULT 1000,
  ean              TEXT,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);
CREATE UNIQUE INDEX products_user_ean ON products(user_id, ean) WHERE ean IS NOT NULL;
CREATE INDEX products_user ON products(user_id);
CREATE INDEX products_brand_norm ON products(brand_norm);

CREATE TABLE spools (
  id                  INTEGER PRIMARY KEY,
  user_id             INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  product_id          INTEGER NOT NULL REFERENCES products(id),
  level               TEXT NOT NULL DEFAULT 'new',
  purchase_date       TEXT,
  price_cents         INTEGER,
  supplier            TEXT,
  notes               TEXT,
  hidden_from_friends INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL
);
CREATE INDEX spools_user ON spools(user_id);
CREATE INDEX spools_product ON spools(product_id);
