-- Identifiant WebAuthn opaque de l'utilisateur (« user handle »), créé à la première passkey.
ALTER TABLE users ADD COLUMN webauthn_id BLOB;
CREATE UNIQUE INDEX users_webauthn_id ON users(webauthn_id) WHERE webauthn_id IS NOT NULL;

CREATE TABLE webauthn_credentials (
  id            INTEGER PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  credential_id BLOB NOT NULL UNIQUE,
  name          TEXT NOT NULL,
  data          TEXT NOT NULL,   -- JSON de webauthn.Credential (clé publique, compteur, indicateurs)
  created_at    TEXT NOT NULL,
  last_used_at  TEXT
);
CREATE INDEX webauthn_credentials_user ON webauthn_credentials(user_id);
