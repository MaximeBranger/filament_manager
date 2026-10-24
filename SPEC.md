# FilamentManager — Spécification

Application web ultra légère pour suivre son stock de bobines de filament 3D, partager sa collection avec des amis, prêter et donner des bobines.

Ce document est la référence pour l'implémentation. Les décisions qu'il contient ont été arrêtées ; toute évolution doit être reportée ici.

---

## 1. Périmètre et lots

| Lot | Contenu |
|---|---|
| **v1a** | Comptes (lien magique), produits & bobines, liste / recherche / filtres / couleur proche, scan code-barre (unitaire + réception en série), export & suppression de compte, sauvegardes, Plausible |
| **v1b** | Amis (invitation par lien), consultation du stock des amis, prêts, dons, notifications (in-app + email) |
| **v2** | Passkeys (WebAuthn), PWA (manifest, service worker, installable) |

Hors périmètre v1 : base EAN externe, catalogue de produits partagé, dates de retour / relances de prêt, captcha, multilingue (FR uniquement), tests E2E navigateur.

---

## 2. Concepts

- **Produit** : une référence commerciale (marque + matériau + couleur + finition + diamètre + poids, éventuellement un EAN). Appartient à **un** utilisateur. Sert à ne pas ressaisir les infos et à préremplir au scan.
- **Bobine** : un objet physique. Pointe vers un produit. Porte ce qui est propre à l'exemplaire : niveau, date d'achat, prix, fournisseur, notes, visibilité. **Une ligne par bobine** : 3 bobines identiques = 3 lignes.
- **Ami** : relation réciproque créée via un lien d'invitation.
- **Prêt** : une bobine reste la propriété du prêteur, elle est marquée « prêtée à X ».
- **Don** : la propriété de la bobine est transférée à un ami.

Modifier un produit modifie toutes ses bobines (l'UI l'indique : « s'applique à N bobines »).

---

## 3. Référentiels (définis dans le code)

### 3.1 Niveau de bobine
`new` (neuf) · `started` (entamée) · `half` (moitié) · `low` (faible) · `empty` (vide)

### 3.2 Matériau
`PLA`, `PLA+`, `PETG`, `ABS`, `ASA`, `TPU`, `PA` (nylon), `PC`, `PVA`, `HIPS`, `OTHER` (+ champ libre `material_other`).

### 3.3 Finition
`standard`, `matte`, `silk`, `glossy`, `glitter` (pailleté), `marble`, `wood`, `carbon` (fibre de carbone), `glow` (phosphorescent), `multicolor` (multicolore / dégradé), `OTHER` (+ champ libre `finish_other`).

### 3.4 Famille de couleur (avec hexa par défaut)

| Code | Libellé | Hexa par défaut |
|---|---|---|
| `black` | Noir | `#1a1a1a` |
| `white` | Blanc | `#f5f5f5` |
| `grey` | Gris | `#8a8a8a` |
| `red` | Rouge | `#c62828` |
| `orange` | Orange | `#ef6c00` |
| `yellow` | Jaune | `#fbc02d` |
| `green` | Vert | `#2e7d32` |
| `blue` | Bleu | `#1565c0` |
| `purple` | Violet | `#6a1b9a` |
| `pink` | Rose | `#ec407a` |
| `brown` | Marron | `#6d4c41` |
| `beige` | Beige | `#d7c4a3` |
| `transparent` | Transparent | `#e0f2f1` |
| `multicolor` | Multicolore | `#9e9e9e` |

### 3.5 Diamètre
`1.75` (défaut) ou `2.85` (mm).

### 3.6 Poids nominal
Saisie en grammes, avec raccourcis : 250, 500, 750, 1000 (défaut), 2000, 3000.

### 3.7 Marques
Texte libre. Suggestions d'autocomplétion = union de :
1. Une liste de départ embarquée (~30 marques : Bambu Lab, Prusament, eSun, Sunlu, Polymaker, Elegoo, Eryone, Geeetech, Jayo, Kingroon, Creality, Anycubic, Overture, Fiberlogy, Spectrum, Extrudr, Fillamentum, 3DJake, Das Filament, Sakata, ColorFabb, Formfutura, Rosa3D, Azurefilm, Atomic, Hatchbox, Inland, Amolen, Kexcelled, Ziro…).
2. Les marques de l'utilisateur courant.
3. Les marques des autres utilisateurs utilisées par **≥ 3 comptes distincts**.

Comparaison normalisée (`brand_norm`) : minuscules, sans accents, espaces multiples réduits, trim. À la saisie, si une marque existante a le même `brand_norm`, on réutilise sa graphie.

---

## 4. Modèle de données (SQLite)

Conventions : `id INTEGER PRIMARY KEY`, dates en texte ISO 8601 UTC (`created_at`, etc.), dates calendaires en `YYYY-MM-DD`, prix en centimes d'euro, booléens en `INTEGER 0/1`. `PRAGMA foreign_keys = ON`, `journal_mode = WAL`, `busy_timeout = 5000`.

```sql
-- Comptes ------------------------------------------------------------------
CREATE TABLE users (
  id                  INTEGER PRIMARY KEY,
  email               TEXT NOT NULL UNIQUE COLLATE NOCASE,
  display_name        TEXT NOT NULL,
  email_notifications INTEGER NOT NULL DEFAULT 1,
  created_at          TEXT NOT NULL
);

CREATE TABLE sessions (
  id_hash      TEXT PRIMARY KEY,          -- SHA-256 du jeton de session (le cookie porte le jeton brut)
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);

CREATE TABLE login_challenges (
  id           INTEGER PRIMARY KEY,
  email        TEXT NOT NULL COLLATE NOCASE,
  token_hash   TEXT NOT NULL UNIQUE,      -- lien magique
  code_hash    TEXT NOT NULL,             -- code 6 chiffres
  browser_hash TEXT NOT NULL,             -- lie le code au navigateur demandeur (cookie)
  attempts     INTEGER NOT NULL DEFAULT 0,
  next_path    TEXT,                      -- redirection après connexion (ex. invitation)
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  used_at      TEXT
);

-- v2 : réservé pour les passkeys
-- CREATE TABLE webauthn_credentials (...);

-- Catalogue & stock ----------------------------------------------------------
CREATE TABLE products (
  id               INTEGER PRIMARY KEY,
  user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  brand            TEXT NOT NULL,
  brand_norm       TEXT NOT NULL,
  material         TEXT NOT NULL,
  material_other   TEXT,
  color_name       TEXT NOT NULL,
  color_family     TEXT NOT NULL,
  color_hex        TEXT,                  -- NULL => hexa par défaut de la famille
  finish           TEXT NOT NULL DEFAULT 'standard',
  finish_other     TEXT,
  diameter_mm      REAL NOT NULL DEFAULT 1.75,
  nominal_weight_g INTEGER NOT NULL DEFAULT 1000,
  ean              TEXT,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);
CREATE UNIQUE INDEX products_user_ean ON products(user_id, ean) WHERE ean IS NOT NULL;
CREATE INDEX products_brand_norm ON products(brand_norm);

CREATE TABLE spools (
  id                  INTEGER PRIMARY KEY,
  user_id             INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,  -- propriétaire
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

-- Amis -------------------------------------------------------------------------
CREATE TABLE friend_invitations (
  id          INTEGER PRIMARY KEY,
  inviter_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash  TEXT NOT NULL UNIQUE,
  created_at  TEXT NOT NULL,
  expires_at  TEXT NOT NULL,              -- +7 jours
  used_at     TEXT,
  used_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
  revoked_at  TEXT
);

CREATE TABLE friendships (
  user_a_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_b_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY (user_a_id, user_b_id),
  CHECK (user_a_id < user_b_id)
);

-- Prêts & dons -------------------------------------------------------------------
-- Une "transaction" regroupe une demande/proposition portant sur 1..n bobines.
CREATE TABLE transfers (
  id            INTEGER PRIMARY KEY,
  kind          TEXT NOT NULL,            -- 'loan' | 'gift'
  owner_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,  -- propriétaire des bobines
  other_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,  -- emprunteur / receveur
  initiated_by  TEXT NOT NULL,            -- 'owner' | 'other'
  status        TEXT NOT NULL,            -- 'pending' | 'accepted' | 'refused' | 'cancelled'
  message       TEXT,
  grams_needed  INTEGER,                  -- indicatif, demandes de prêt
  created_at    TEXT NOT NULL,
  decided_at    TEXT
);

CREATE TABLE transfer_items (
  transfer_id  INTEGER NOT NULL REFERENCES transfers(id) ON DELETE CASCADE,
  spool_id     INTEGER NOT NULL REFERENCES spools(id) ON DELETE CASCADE,
  decision     TEXT,                      -- NULL (en attente) | 'accepted' | 'refused'
  PRIMARY KEY (transfer_id, spool_id)
);

CREATE TABLE loans (
  id             INTEGER PRIMARY KEY,
  spool_id       INTEGER NOT NULL REFERENCES spools(id) ON DELETE CASCADE,
  lender_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  borrower_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  transfer_id    INTEGER REFERENCES transfers(id) ON DELETE SET NULL,
  started_at     TEXT NOT NULL,
  ended_at       TEXT,
  end_reason     TEXT                     -- 'returned' | 'gifted'
);
CREATE UNIQUE INDEX loans_active_spool ON loans(spool_id) WHERE ended_at IS NULL;

-- Trace des dons côté donneur (la bobine a changé de propriétaire)
CREATE TABLE gifts (
  id             INTEGER PRIMARY KEY,
  spool_id       INTEGER REFERENCES spools(id) ON DELETE SET NULL,
  giver_id       INTEGER REFERENCES users(id) ON DELETE SET NULL,
  receiver_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  label          TEXT NOT NULL,           -- instantané lisible : "eSun PLA+ Galaxy Black 1 kg"
  level          TEXT NOT NULL,
  given_at       TEXT NOT NULL
);

-- Notifications --------------------------------------------------------------------
CREATE TABLE notifications (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL,              -- voir §8.4
  payload     TEXT NOT NULL,              -- JSON
  created_at  TEXT NOT NULL,
  read_at     TEXT
);
CREATE INDEX notifications_user_unread ON notifications(user_id) WHERE read_at IS NULL;
```

Règles d'intégrité applicatives (vérifiées dans le code + tests) :
- `spools.product_id` doit appartenir au même `user_id` que la bobine.
- Une bobine en prêt actif ne peut ni être supprimée, ni changer de niveau, ni être incluse dans une autre transaction.
- Un produit sans bobine reste (utile pour le scan). Suppression manuelle possible si aucune bobine.

---

## 5. Authentification & comptes

### 5.1 Connexion par lien magique (inscription = première connexion)
1. L'utilisateur saisit son email. Réponse **toujours identique** (« si l'adresse est valide, un email est parti ») pour ne pas révéler l'existence d'un compte.
2. Création d'un `login_challenge` : jeton aléatoire 32 octets (lien) + code 6 chiffres, valables `MAGIC_LINK_TTL` (15 min). Un cookie `login_browser` (aléatoire) est posé ; son hash est stocké dans `browser_hash`.
3. Email envoyé avec le lien `BASE_URL/auth/verify?t=…` **et** le code.
4. Validation :
   - **Lien** : valide depuis n'importe quel navigateur.
   - **Code** : valide uniquement depuis le navigateur demandeur (cookie `login_browser`), 5 tentatives max puis challenge invalidé.
5. Challenge à usage unique. Si l'email n'a pas de compte : écran « Choisis un nom d'affichage » puis création. La validation vaut vérification de l'email.
6. Redirection vers `next_path` (ex. page d'invitation) si présent.

### 5.2 Sessions
- Cookie `session` : jeton aléatoire 32 octets, `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`. Stocké hashé en base.
- Durée `SESSION_TTL` (30 j), glissante (renouvelée si `last_seen_at` > 24 h).
- Déconnexion = suppression de la session.

### 5.3 Limitation des envois (en mémoire, sans dépendance)
- Par email : `RATE_LIMIT_EMAIL` (défaut 3 / 15 min).
- Par IP : `RATE_LIMIT_IP` (défaut 20 / 15 min). IP réelle lue via `X-Forwarded-For` **uniquement** si la requête vient de `TRUSTED_PROXIES`.
- Dépassement : même réponse générique côté UI, log `warn`.

### 5.4 Compte
- Modifier nom d'affichage, préférence emails.
- Changer d'email : lien magique envoyé à la nouvelle adresse.
- **Export** : JSON (et CSV des bobines) de toutes ses données.
- **Suppression** : confirmation par saisie de l'email. **Refusée tant qu'un prêt actif existe** (comme prêteur ou emprunteur) ; transactions en attente annulées. Suppression réelle (cascade).

### 5.5 v2 — Passkeys
WebAuthn en complément, le lien magique restant le moyen de récupération. Variables prévues : `WEBAUTHN_RP_ID`, `WEBAUTHN_RP_NAME`.

---

## 6. Stock

### 6.1 Écran principal (liste)
- Liste compacte mobile-first. Ligne : pastille couleur · nom couleur · marque · matériau · finition · badge niveau · « prêtée à X » le cas échéant.
- **Changement de niveau depuis la liste** (tap sur le badge → choix) via htmx.
- **Recherche texte** unique sur marque, nom de couleur, matériau (+ autre), finition (+ autre), notes. Insensible casse/accents.
- **Filtres combinables** : famille de couleur (pastilles), matériau, marque, finition, niveau, disponibilité (disponible / prêtée), diamètre.
- **Bobines vides masquées par défaut** ; filtre « afficher les vides ».
- **Tri** : couleur (teinte HSL), marque, date d'achat, niveau.
- **État dans l'URL** (`?q=&fam=blue&mat=PETG&lvl=…&sort=…`) : favoris possibles, retour arrière cohérent.
- Compteurs : nombre de bobines affichées / total.

### 6.2 Recherche par couleur proche
- Sélecteur de couleur → hexa cible.
- Distance **ΔE2000** (sRGB → XYZ D65 → CIELAB) entre la cible et `COALESCE(color_hex, hexa par défaut de la famille)`.
- Tolérance : très proche (ΔE ≤ 5), proche (≤ 10), large (≤ 20). Tri par ΔE croissant.
- Calcul en Go à la volée sur les bobines de la vue courante (volumes faibles), combinable avec les autres filtres.

### 6.3 Fiche produit / bobine
- Champs produit (§3) + champs bobine : niveau, date d'achat, prix (€), fournisseur, notes, masquée aux amis.
- Le sélecteur hexa est pré-réglé sur la couleur par défaut de la famille choisie.
- Édition d'un produit : avertissement « s'applique à N bobines ».
- Ajout : choisir un produit existant ou en créer un ; champ **quantité** (crée N bobines identiques).
- Suppression d'une bobine : confirmation ; refusée si prêt actif.

### 6.4 Scan code-barre
- Bibliothèque **zxing-js** (servie localement, pas de CDN), fonctionne sur Chrome, Safari iOS, Firefox. HTTPS requis (caméra).
- Formats : EAN-13, EAN-8, UPC-A, UPC-E, Code 128.
- Saisie manuelle de l'EAN possible (lecteur USB / caméra indisponible).
- **Mode unitaire** : scan → EAN connu dans *mes* produits : formulaire bobine prérempli ; inconnu : formulaire produit vierge avec EAN rempli.
- **Mode réception en série** : caméra continue, liste temporaire côté client.
  - EAN connu → ligne ajoutée (quantité 1, re-scan = +1, quantité éditable).
  - EAN inconnu → mini-formulaire produit, puis reprise du scan.
  - Anti-doublon : un même code lu en continu n'est compté qu'une fois tant qu'il n'a pas quitté le champ (~1,5 s).
  - Validation finale : date d'achat commune (défaut aujourd'hui), fournisseur commun optionnel → création en une transaction.

---

## 7. Amis

### 7.1 Invitation
- « Inviter un ami » génère un lien `BASE_URL/invite/<token>` : usage unique, expire après 7 jours, révocable depuis la liste des invitations en cours.
- À l'ouverture : si non connecté → connexion / inscription par lien magique, avec retour sur l'invitation (`next_path`).
- Écran de confirmation « *X* t'invite à partager vos stocks — Accepter / Refuser ».
- Acceptation → `friendships` créée (réciproque), invitation consommée. Refus/inviter soi-même/déjà amis : message explicite, rien n'est créé.
- Retirer un ami : coupe la relation dans les deux sens. Refusé tant qu'un prêt actif existe entre les deux ; transactions en attente annulées.

### 7.2 Visibilité
Un ami voit les bobines de l'autre **sauf** : niveau `empty`, `hidden_from_friends = 1`.
Champs visibles : produit complet, niveau, statut de prêt (« prêtée » sans préciser à qui, sauf si c'est à lui).
Champs **jamais** visibles : date d'achat, prix, fournisseur, notes.

### 7.3 Vue « stock des amis »
Même écran que §6.1 avec un sélecteur de périmètre : *Mon stock* / *Stock de X* / *Tous mes amis*. Sélection multiple de bobines → « Demander un prêt » / « Demander un don ».

---

## 8. Prêts & dons

### 8.1 Prêt
1. **Demande** (initiée par l'emprunteur) : 1..n bobines d'un même ami, message optionnel, grammes nécessaires optionnel. → `transfers(kind='loan', initiated_by='other', status='pending')`.
2. **Décision du prêteur** par bobine (acceptation partielle possible). Transaction `accepted` si au moins une bobine acceptée, sinon `refused`. Chaque bobine acceptée → `loans` actif.
3. **Prêt direct** (raccourci) : le prêteur enregistre un prêt à un ami sans demande → transaction créée directement `accepted`, pas d'acceptation requise.
4. Pendant le prêt : niveau **figé** ; la bobine apparaît chez l'emprunteur dans une section « Empruntées » (hors de son stock).
5. **Retour** : marqué par le **prêteur**, qui saisit le nouveau niveau à ce moment-là. `loans.ended_at`, `end_reason='returned'`.
6. Le demandeur peut annuler une demande `pending`.

### 8.2 Don
1. **Proposition** par le propriétaire (`initiated_by='owner'`) ou **demande** par l'ami (`initiated_by='other'`) — même formulaire que le prêt avec un choix Prêt / Don.
2. L'autre partie **doit accepter** (dans les deux sens).
3. **Conversion d'un prêt en don** (« garde-la ») : action du prêteur sur un prêt actif, effet immédiat, pas d'acceptation de l'emprunteur. `loans.end_reason='gifted'`.
4. À la finalisation, dans une transaction SQL :
   - Le donneur indique le niveau actuel (pré-rempli).
   - Produit cible chez le receveur : produit existant avec le même EAN, sinon **copie** du produit (marque, matériau, couleur, hexa, finition, diamètre, poids, EAN).
   - La bobine change de `user_id` / `product_id` ; `purchase_date`, `price_cents`, `supplier`, `notes` sont **effacés** ; `hidden_from_friends = 0`.
   - Ligne `gifts` écrite (historique « Donnée à X le … » côté donneur, « Reçue de X le … » côté receveur).

### 8.3 Écran « Prêts & dons »
Onglets : Demandes reçues · Demandes envoyées · Prêtées · Empruntées · Historique (prêts terminés, dons).

### 8.4 Notifications
Badge in-app (compteur non lus) + email si `email_notifications = 1`.

| Événement | Destinataire | Email |
|---|---|---|
| `invite_accepted` | invitant | oui |
| `loan_requested` / `gift_requested` | propriétaire | oui |
| `gift_offered` | receveur | oui |
| `transfer_accepted` / `transfer_refused` | initiateur | oui |
| `loan_recorded` (prêt direct) | emprunteur | oui |
| `loan_returned` | emprunteur | non |
| `loan_converted_to_gift` | emprunteur | oui |
| `friend_removed` | — | aucune notification |

Les emails sont envoyés de façon asynchrone (goroutine + file en mémoire, quelques retries). Échec = log `error`, la notification in-app reste.

---

## 9. Architecture technique

### 9.1 Stack
- **Go** (dernière version stable), un seul binaire.
- HTTP : `net/http` (routeur standard Go ≥ 1.22).
- Rendu : `html/template` + **htmx** (servi localement). JS maison minimal : scanner, sélecteur de couleur, liste de réception.
- SQLite : `modernc.org/sqlite` (pur Go, **sans CGO**). Requêtes typées via **sqlc**.
- Migrations SQL versionnées, **embarquées** (`embed`), appliquées automatiquement au démarrage, uniquement ascendantes.
- Templates, CSS, JS, migrations embarqués dans le binaire.
- CSS : feuille maison légère, mobile-first, pas de framework ni d'étape de build front.

### 9.2 Arborescence proposée
```
cmd/filamentmanager/main.go     # config, wiring, démarrage
internal/config/                # lecture + validation des env vars
internal/db/                    # ouverture SQLite, migrations, code sqlc
internal/db/migrations/*.sql
internal/db/queries/*.sql
internal/auth/                  # lien magique, sessions, rate limit
internal/stock/                 # produits, bobines, recherche, filtres
internal/color/                 # conversion Lab, ΔE2000, familles
internal/friends/               # invitations, amitiés, visibilité
internal/transfers/             # prêts, dons
internal/notify/                # notifications in-app + email
internal/mail/                  # interface Mailer + implémentation SMTP
internal/backup/                # VACUUM INTO périodique + rétention
internal/web/                   # handlers HTTP, middlewares, templates
web/templates/  web/static/     # embarqués
```

### 9.3 Sécurité
- **Cloisonnement** : toute requête de lecture/écriture filtre sur `user_id` de la session ; l'accès au stock d'un tiers passe par une seule fonction de vérification d'amitié + règles §7.2. Couvert par des tests dédiés.
- **CSRF** : cookie `SameSite=Lax` + jeton CSRF synchronisé, envoyé par htmx via `hx-headers` et dans les formulaires.
- En-têtes : `Content-Security-Policy` stricte (`default-src 'self'`, + origine Plausible si activé), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `Permissions-Policy: camera=(self)`.
- Jetons (session, lien magique, invitation) : 32 octets `crypto/rand`, stockés hashés (SHA-256), comparaison en temps constant.
- Aucune donnée personnelle dans les URL suivies par Plausible.

### 9.4 Sauvegardes
- Si `BACKUP_DIR` défini : `VACUUM INTO '<BACKUP_DIR>/filamentmanager-<horodatage>.db'` toutes les `BACKUP_INTERVAL`, conservation des `BACKUP_RETENTION` derniers fichiers.
- `BACKUP_DIR` sur un volume distinct de `/data`, récupéré par la sauvegarde externe quotidienne du serveur.

### 9.5 Observabilité
- Logs JSON sur stdout (`log/slog`), niveau `LOG_LEVEL`. Jamais de jeton ni de code dans les logs.
- `GET /healthz` : processus vivant. `GET /readyz` : base accessible (`SELECT 1`).
- Plausible injecté uniquement si `PLAUSIBLE_URL` et `PLAUSIBLE_DOMAIN` sont définis ; mentionné dans la page confidentialité.

### 9.6 Pages légales
`/confidentialite` : données collectées, finalités, Plausible (sans cookie), durée de conservation, suppression & export en libre-service, contact.

---

## 10. Configuration (variables d'environnement)

L'application **refuse de démarrer** si une variable obligatoire manque ou est invalide, avec un message explicite.

| Variable | Rôle | Défaut |
|---|---|---|
| `BASE_URL` | URL publique (liens emails, cookies `Secure`) | **obligatoire** |
| `LISTEN_ADDR` | Adresse d'écoute | `:8080` |
| `DATABASE_PATH` | Fichier SQLite | `/data/app.db` |
| `SESSION_SECRET` | Clé de signature (CSRF, cookies) — ≥ 32 octets | **obligatoire** |
| `SESSION_TTL` | Durée de session | `720h` |
| `SMTP_HOST` | Serveur SMTP | **obligatoire** |
| `SMTP_PORT` | Port SMTP | `587` |
| `SMTP_USER` / `SMTP_PASSWORD` | Identifiants SMTP | vide |
| `SMTP_FROM` | Expéditeur, ex. `FilamentManager <noreply@exemple.fr>` | **obligatoire** |
| `SMTP_TLS` | `starttls` \| `tls` \| `none` | `starttls` |
| `MAGIC_LINK_TTL` | Validité lien / code | `15m` |
| `RATE_LIMIT_EMAIL` | Envois par email | `3/15m` |
| `RATE_LIMIT_IP` | Envois par IP | `20/15m` |
| `TRUSTED_PROXIES` | IP/CIDR des proxys de confiance (Nginx Proxy Manager) | vide |
| `BACKUP_DIR` | Dossier de sauvegarde (vide = désactivé) | vide |
| `BACKUP_INTERVAL` | Fréquence | `6h` |
| `BACKUP_RETENTION` | Nombre de fichiers conservés | `28` |
| `PLAUSIBLE_URL` | URL de l'instance Plausible | vide |
| `PLAUSIBLE_DOMAIN` | Domaine déclaré dans Plausible | vide |
| `LOG_LEVEL` | `debug` \| `info` \| `warn` \| `error` | `info` |

---

## 11. Build, CI/CD, déploiement

- **Image Docker** multi-étapes : build Go (`CGO_ENABLED=0`) → image finale distroless/static non-root. Volumes : `/data` (base), `/backups` (si `BACKUP_DIR=/backups`). Port exposé : 8080.
- **CI GitHub Actions** : `go vet`, `go test ./...`, build et push de l'image vers Nexus. *Modèle de workflow et stratégie de tags à fournir par Maxime lors de la mise en place.*
- **Production** : une seule instance (contrainte SQLite). TLS terminé par **Nginx Proxy Manager** sur le serveur ; l'application écoute en HTTP. `TRUSTED_PROXIES` = IP/sous-réseau Docker de NPM.

---

## 12. Tests

- **Unitaires** : ΔE2000 (jeux de référence publiés de Sharma et al.), conversion hexa → Lab, normalisation des marques, machine à états des transferts (pending → accepted/refused/cancelled ; loan → returned/gifted), règles de visibilité, rate limiter.
- **Intégration HTTP** (`httptest` + SQLite temporaire par test + `Mailer` factice qui capture les emails) :
  - inscription / connexion par lien et par code (dont code depuis un autre navigateur = refus, 5 tentatives) ;
  - **cloisonnement** : un utilisateur ne peut lire ni modifier les bobines/produits d'un non-ami, ni les champs privés d'un ami ;
  - invitation (usage unique, expiration, révocation) ;
  - prêt (demande, acceptation partielle, niveau figé, retour) ;
  - don (proposition, demande, conversion de prêt, copie de produit, effacement des champs privés) ;
  - suppression de compte (refus si prêt actif, cascade sinon) ;
  - réception en série (création de N bobines en une transaction).
- Pas de tests E2E navigateur en v1.

---

## 13. Points ouverts

- Modèle GitHub Actions et stratégie de tags d'image (à fournir).
- Nom de domaine → `BASE_URL`, `SMTP_FROM`, `PLAUSIBLE_DOMAIN`.
