CREATE TABLE friend_invitations (
  id          INTEGER PRIMARY KEY,
  inviter_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash  TEXT NOT NULL UNIQUE,
  created_at  TEXT NOT NULL,
  expires_at  TEXT NOT NULL,
  used_at     TEXT,
  used_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
  revoked_at  TEXT
);
CREATE INDEX friend_invitations_inviter ON friend_invitations(inviter_id);

CREATE TABLE friendships (
  user_a_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_b_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY (user_a_id, user_b_id),
  CHECK (user_a_id < user_b_id)
);
CREATE INDEX friendships_b ON friendships(user_b_id);

CREATE TABLE transfers (
  id            INTEGER PRIMARY KEY,
  kind          TEXT NOT NULL,
  owner_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  other_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  initiated_by  TEXT NOT NULL,
  status        TEXT NOT NULL,
  message       TEXT,
  grams_needed  INTEGER,
  created_at    TEXT NOT NULL,
  decided_at    TEXT
);
CREATE INDEX transfers_owner ON transfers(owner_id);
CREATE INDEX transfers_other ON transfers(other_id);

CREATE TABLE transfer_items (
  transfer_id  INTEGER NOT NULL REFERENCES transfers(id) ON DELETE CASCADE,
  spool_id     INTEGER NOT NULL REFERENCES spools(id) ON DELETE CASCADE,
  decision     TEXT,
  PRIMARY KEY (transfer_id, spool_id)
);
CREATE INDEX transfer_items_spool ON transfer_items(spool_id);

CREATE TABLE loans (
  id             INTEGER PRIMARY KEY,
  spool_id       INTEGER NOT NULL REFERENCES spools(id) ON DELETE CASCADE,
  lender_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  borrower_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  transfer_id    INTEGER REFERENCES transfers(id) ON DELETE SET NULL,
  started_at     TEXT NOT NULL,
  ended_at       TEXT,
  end_reason     TEXT
);
CREATE UNIQUE INDEX loans_active_spool ON loans(spool_id) WHERE ended_at IS NULL;
CREATE INDEX loans_lender ON loans(lender_id);
CREATE INDEX loans_borrower ON loans(borrower_id);

CREATE TABLE gifts (
  id           INTEGER PRIMARY KEY,
  spool_id     INTEGER REFERENCES spools(id) ON DELETE SET NULL,
  giver_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
  receiver_id  INTEGER REFERENCES users(id) ON DELETE SET NULL,
  label        TEXT NOT NULL,
  level        TEXT NOT NULL,
  given_at     TEXT NOT NULL
);
CREATE INDEX gifts_giver ON gifts(giver_id);
CREATE INDEX gifts_receiver ON gifts(receiver_id);

CREATE TABLE notifications (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL,
  payload     TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  read_at     TEXT
);
CREATE INDEX notifications_user ON notifications(user_id, id);
