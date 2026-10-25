# FilamentManager — Spécification

Application web ultra légère pour suivre son stock de bobines de filament 3D, partager sa collection avec des amis, prêter et donner des bobines.

Ce document est la référence pour l'implémentation. Les décisions qu'il contient ont été arrêtées ; toute évolution doit être reportée ici.

---

## 1. Périmètre et lots

| Lot | Contenu |
|---|---|
| **v1a** | Comptes (lien magique), produits & bobines, liste / recherche / filtres / couleur proche, scan code-barre (unitaire + réception en série), export & suppression de compte, sauvegardes, Plausible |
| **v1b** | Amis (invitation par lien), consultation du stock des amis, prêts, dons, notifications (in-app + email) |
| **v1c** | Partage de modèle 3D : envoi d'un STL, lien public temporaire (mot de passe facultatif), aperçu 3D dans les couleurs du stock, choix du destinataire notifié (§9) |
| **v2** | Passkeys (WebAuthn), PWA (manifest, service worker, installable) |

Hors périmètre v1 : API EAN externe interrogée à l'exécution (la référence des codes-barres est embarquée, §6.5), catalogue de produits partagé, dates de retour / relances de prêt, captcha, multilingue (FR uniquement), tests E2E navigateur.

Hors périmètre v1c : 3MF et rendu multicolore, plusieurs fichiers par partage, textures procédurales (bois, marbre, carbone), import depuis un dépôt de modèles (§14).

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

Les listes et filtres présentent les familles par ordre alphabétique de libellé. Un produit multicolore peut porter une seconde teinte (`color_hex2`) : sa pastille est alors coupée en diagonale entre les deux teintes.

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
  color_hex2       TEXT,                  -- seconde teinte, famille multicolor uniquement
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
CREATE INDEX notifications_user ON notifications(user_id, id);
-- Les migrations ajoutent des index sur les clés étrangères (friendships.user_b_id,
-- transfer_items.spool_id, loans.lender_id/borrower_id, gifts.giver_id/receiver_id…).

-- Partage de modèles 3D (v1c) ----------------------------------------------------
CREATE TABLE shares (
  id               INTEGER PRIMARY KEY,
  user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash       TEXT NOT NULL UNIQUE,     -- SHA-256 du jeton du lien
  title            TEXT NOT NULL,            -- nom du modèle affiché au destinataire
  password_hash    TEXT,                     -- NULL = pas de mot de passe ; "pbkdf2-sha256$<iter>$<sel>$<hash>"
  password_version INTEGER NOT NULL DEFAULT 0, -- incrémenté à chaque changement : invalide les cookies d'accès
  failed_attempts  INTEGER NOT NULL DEFAULT 0,
  locked_at        TEXT,                     -- verrouillé après 20 échecs, définitif (révoquer et recréer)
  created_at       TEXT NOT NULL,
  expires_at       TEXT NOT NULL             -- created_at + 7 jours
);
CREATE INDEX shares_user ON shares(user_id);
CREATE INDEX shares_expires ON shares(expires_at);

-- Un seul fichier par partage en v1c ; la table anticipe le multi-fichiers.
CREATE TABLE share_files (
  id             INTEGER PRIMARY KEY,
  share_id       INTEGER NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
  original_name  TEXT NOT NULL,             -- nom du STL envoyé (affichage uniquement)
  storage_key    TEXT NOT NULL UNIQUE,      -- nom aléatoire du maillage converti dans SHARE_DIR
  triangles      INTEGER NOT NULL,
  size_x_mm      REAL NOT NULL,
  size_y_mm      REAL NOT NULL,
  size_z_mm      REAL NOT NULL,
  bytes          INTEGER NOT NULL,
  created_at     TEXT NOT NULL
);
CREATE INDEX share_files_share ON share_files(share_id);

-- Choix du destinataire (un seul par partage, modifiable jusqu'à l'expiration).
CREATE TABLE share_choices (
  share_id        INTEGER PRIMARY KEY REFERENCES shares(id) ON DELETE CASCADE,
  product_id      INTEGER REFERENCES products(id) ON DELETE SET NULL,
  label           TEXT NOT NULL,            -- instantané : "eSun PLA+ Galaxy Black (silk)"
  color_hex       TEXT NOT NULL,            -- instantané
  recipient_name  TEXT,                     -- facultatif, ≤ 50 caractères
  message         TEXT,                     -- facultatif, ≤ 500 caractères
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);
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
- « Mon compte » indique le nombre d'autres sessions ouvertes et permet de les fermer toutes (appareil perdu, session volée).
- **Connexion récente** : ajouter ou supprimer une passkey et supprimer le compte exigent une session ouverte il y a moins de 15 min. Sinon, renvoi vers `/login?reauth=1&next=/account` (email prérempli) ; la nouvelle connexion remplace la session du navigateur.

### 5.3 Limitation des envois (en mémoire, sans dépendance)
- Par email : `RATE_LIMIT_EMAIL` (défaut 3 / 15 min).
- Par IP : `RATE_LIMIT_IP` (défaut 20 / 15 min). IP réelle lue via `X-Forwarded-For` **uniquement** si la requête vient de `TRUSTED_PROXIES`.
- Dépassement : même réponse générique côté UI, log `warn`.

### 5.4 Compte
- Modifier nom d'affichage, préférence emails.
- Changer d'email : lien magique envoyé à la nouvelle adresse. *(Reporté après v1a.)*
- **Export** : JSON (et CSV des bobines) de toutes ses données.
- **Suppression** : confirmation par saisie de l'email. **Refusée tant qu'un prêt actif existe** (comme prêteur ou emprunteur) ; transactions en attente annulées. Suppression réelle (cascade).

### 5.5 v2 — Passkeys
WebAuthn en complément, le lien magique restant le moyen de récupération. Bibliothèque `github.com/go-webauthn/webauthn` (pas de vérification cryptographique maison).
- **Ajout** depuis « Mon compte » : nom de l'appareil (≤ 50 caractères), passkey **découvrable** obligatoire (`residentKey: required`), vérification de l'utilisateur préférée, attestation `none`, exclusion des passkeys déjà enregistrées. Liste avec dates d'ajout et de dernière utilisation, suppression. Chaque ajout déclenche un email d'alerte, envoyé même si les notifications sont désactivées.
- **Connexion** sans saisir d'email : bouton « Se connecter avec une passkey » et suggestion du navigateur dans le champ email (médiation conditionnelle, `autocomplete="email webauthn"`). Le paramètre `next` est conservé.
- **Défis** : gardés en mémoire côté serveur (instance unique), à usage unique, 5 min, référencés par un cookie `webauthn`. Limite de 30 débuts de connexion / 15 min par IP.
- **Contrôles** : origine = `BASE_URL`, RP ID = `WEBAUTHN_RP_ID` ; compteur de signatures mis à jour à chaque connexion, connexion refusée si le compteur recule (passkey possiblement clonée).
- **Données** : `users.webauthn_id` (32 octets aléatoires, créé à la première passkey) et table `webauthn_credentials` (identifiant, nom, enregistrement JSON de la bibliothèque, dates). Suppression en cascade avec le compte.

### 5.6 v2 — Application installable (PWA)
- Manifeste `/manifest.webmanifest` : `start_url` `/stock`, affichage `standalone`, icônes 192 / 512 / maskable, raccourcis Scanner, Réception, Prêts & dons. Icône Apple et `theme-color`.
- Service worker `/sw.js` (portée `/`, version de l'application injectée, `Cache-Control: no-cache`) : pages toujours chargées depuis le réseau (aucune page privée en cache) avec repli sur `/offline` ; fichiers `/static/` en cache d'abord ; anciens caches supprimés à chaque déploiement.
- `/offline` : page sans aucune donnée de compte (elle est mise en cache).

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
  - Plafonds par réception : 100 lignes et 500 bobines au total (la transaction bloque les autres écritures).

---

### 6.5 Référence des codes-barres (Open Filament Database)
- **Source** : [Open Filament Database](https://github.com/OpenFilamentCollective/open-filament-database), base communautaire sous licence MIT. On utilise l'export complet `all.json.gz` de ses releases quotidiennes `dataset-vAAAA.MM.JJ`.
- **Index embarqué** : `internal/reference/ofd.json`, produit par `cmd/ofdimport`. Il ne garde que les références portant un code-barre (≈ 6 000), transposées dans notre référentiel :
  - matériaux : PLA, avec « PLA+ » repéré dans le nom de gamme ; PA6/PA11/PA12/PPA → PA ; les autres → « Autre » avec leur nom ;
  - finition d'après les traits, par priorité : phosphorescent, fibre de carbone, marbre, bois, multicolore (coextrudé, dégradé, plusieurs teintes), pailleté, silk, mat ;
  - famille de couleur déduite de la teinte (TSL), transparente ou multicolore d'après les traits ;
  - diamètres 1,75 et 2,85 mm seulement ; une bobine est préférée à sa recharge quand elles partagent un code.

  Le fichier compte une ligne par code-barre (diffs lisibles), et la licence MIT est embarquée (`LICENSE-OFD`).
- **Aucun appel réseau à l'exécution.** Codes normalisés sur 13 chiffres : un UPC-A de 12 chiffres prend un zéro devant.
- **Usage** : un code présent dans les produits de l'utilisateur prime toujours. Sinon, au scan unitaire comme en réception, la référence **préremplit** le formulaire (mention de la source et de la gamme) ; l'utilisateur vérifie et valide.
- **Écarts** : sur la fiche d'un produit dont le code-barre est connu de la référence, un tableau liste les champs qui diffèrent (marque, matériau, couleur, famille, teinte au-delà de ΔE 3, finition, diamètre, poids). Le bouton « Resynchroniser avec la référence » remplace intégralement ces informations ; le code-barre est conservé et toutes les bobines suivent. La liste « Produits » signale les produits divergents (« ≠ référence ») et propose une resynchronisation groupée. Calcul à l'affichage, rien n'est stocké.
- **Mise à jour** : le workflow `reference-refresh.yml` tourne chaque lundi. Il télécharge la dernière release, convertit, teste, puis ouvre ou met à jour la pull request `reference/ofd-update` si des codes ont changé, et lance la CI sur cette branche. Cela exige le réglage du dépôt « Allow GitHub Actions to create and approve pull requests ». Un export de moins de 1 000 codes est refusé (export suspect).
- **Crédits** : page `/credits` (source, version embarquée, texte de la licence MIT, bibliothèques utilisées), liée depuis le pied de page.

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
| `share_choice` (v1c, à chaque choix ou modification) | créateur du partage | oui |
| `share_locked` (v1c) | créateur du partage | oui |

Les emails sont envoyés de façon asynchrone (goroutine + file en mémoire, quelques retries). Échec = log `error`, la notification in-app reste.

---

## 9. Partage de modèle 3D (v1c)

Permet d'envoyer un modèle 3D à **n'importe qui** (sans compte) via un lien, pour qu'il le visualise dans les couleurs disponibles dans le stock du créateur et indique son choix.

**Principe directeur : ce n'est jamais un outil de stockage de STL.** Un partage vit 7 jours, puis tout est supprimé (fichier, partage, choix). Le STL d'origine n'est jamais conservé : seul un maillage converti l'est, le temps du partage.

### 9.1 Création (utilisateur connecté)
- Écran « Partages » : liste des partages actifs (miniature et titre cliquables vers l'aperçu, fichier et dimensions, expiration, mot de passe oui/non, verrouillé, lien public à copier, choix du destinataire), bouton « Nouveau partage ». Le partage qui vient d'être créé est mis en avant.
- Formulaire : fichier STL (un seul), titre (prérempli avec le nom du fichier), mot de passe facultatif (≥ 6 caractères).
- Avertissement affiché à la création : « Le destinataire peut techniquement récupérer le modèle 3D. »
- Durée **fixe de 7 jours**, non prolongeable (recréer un partage si besoin).
- Lien `BASE_URL/share/<token>` : jeton dérivé par HMAC (`SESSION_SECRET`) d'un aléa de 32 octets (`link_nonce`) ; seule son empreinte sert à la recherche. Le créateur le retrouve à tout moment dans la liste, sans qu'il soit stocké en clair (une fuite de la base seule ne le révèle pas). Si `SESSION_SECRET` change, les liens existants restent valides mais ne sont plus affichables. Rappel de transmettre le mot de passe par un autre canal.
- Actions sur un partage : copier le lien, prévisualiser (vue destinataire), **révoquer** (suppression immédiate du fichier et du partage). Le mot de passe se choisit à la création et ne se modifie plus ensuite.
- Limites : `SHARE_MAX_ACTIVE` partages actifs par utilisateur (défaut 5), `SHARE_MAX_FILE_SIZE` par fichier (défaut 50 Mo), 2 conversions simultanées au plus sur le serveur (chacune lit le fichier en mémoire ; au-delà : « réessayez dans un instant »).

### 9.2 Traitement du STL
À l'envoi, le serveur lit le fichier en mémoire (`http.MaxBytesReader`) et **le convertit** ; tout échec = rejet avec message explicite, rien n'est écrit.
- Formats acceptés : STL binaire (taille = 84 + 50 × n) ou ASCII (`solid … endsolid`).
- Validation : au moins 1 triangle, au plus **1 000 000** triangles, coordonnées finies (pas de NaN / Inf). Unité supposée : mm.
- Normalisation : recentrage en X/Y, Z minimum ramené à 0. Calcul des dimensions (boîte englobante, mm).
- Écriture d'un maillage binaire compact propre à l'application (en-tête `FMM1`, nombre de triangles, positions `float32` non indexées ; normales recalculées côté navigateur) dans `SHARE_DIR/<storage_key>`, via fichier temporaire + renommage.
- Miniature PNG 256 × 256 (`SHARE_DIR/<storage_key>.png`) : vue isométrique en couleur neutre, rendue en Go pur (tampon de profondeur, anticrénelage 2×). Créée avec le partage, régénérée à la demande si absente, effacée avec lui. Visible du créateur seulement.
- Le fichier envoyé par l'utilisateur n'est **jamais** renvoyé tel quel au navigateur.

### 9.3 Couleurs proposées
Calculées **en direct à chaque affichage** à partir du stock du créateur :
- produits ayant au moins une bobine non `empty`, non `hidden_from_friends`, non prêtée (prêt actif) ;
- **une pastille par produit** (plusieurs bobines identiques = une pastille), triées par teinte ;
- informations affichées : nom de couleur, marque, matériau (+ autre), finition (+ autre) et couleur `COALESCE(color_hex, hexa par défaut de la famille)`.
- Jamais affichés : niveau, nombre de bobines, date d'achat, prix, fournisseur, notes.

### 9.4 Vue destinataire (`/share/<token>`)
- En-tête : « *Nom d'affichage* vous propose *Titre* », dimensions du modèle (mm), date d'expiration.
- Aperçu 3D (**Three.js** servi localement, comme zxing / htmx) : rotation / zoom à la souris et au doigt, fond clair / sombre, clic sur une pastille = application de la couleur. Normales « crantées » (`toCreasedNormals`, 30°) : surfaces courbes lissées, arêtes vives conservées. Modules ES importés par chemins relatifs (pas d'import map, qui serait un script inline bloqué par la CSP).
- Avertissement permanent : « Les couleurs et finitions affichées sont indicatives et peuvent différer du rendu réel. »
- Rendu des finitions (paramètres de `MeshPhysicalMaterial`, aucune texture) :

| Finition / famille | Rendu |
|---|---|
| `standard` | semi-mat |
| `matte` | très mat |
| `glossy` | brillant + vernis (clearcoat) |
| `silk` | brillant légèrement métallique |
| `glitter` | brillant + paillettes simulées (bruit léger : variation aléatoire de luminosité par triangle, en couleurs de sommets, le maillage n'ayant pas d'UV) |
| `glow` | interrupteur « vue dans le noir » (émissif, scène sombre) |
| famille `transparent` | translucide |
| `marble`, `wood`, `carbon`, `multicolor`, `OTHER` | couleur de base + mention « rendu non représentatif » |

- **Choix** : bouton « Je choisis cette couleur » → prénom (facultatif, ≤ 50 car.) et message (facultatif, ≤ 500 car.). Un seul choix par partage, modifiable jusqu'à l'expiration (le dernier remplace le précédent). Chaque enregistrement notifie le créateur (`share_choice`, in-app + email). Limité à 10 envois / 15 min par IP et par partage (en mémoire, §5.3).
- Partage expiré, révoqué ou inconnu : page unique « Ce partage n'est plus disponible », sans révéler le créateur.
- En-têtes propres aux pages de partage : `X-Robots-Tag: noindex`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`.

### 9.5 Mot de passe (facultatif)
- Stocké hashé **PBKDF2-SHA256** (`crypto/pbkdf2`, bibliothèque standard), 600 000 itérations, sel aléatoire 16 octets. Non relisible : en cas d'oubli, le créateur en définit un nouveau.
- Sans saisie valide, le destinataire ne voit qu'une page neutre (ni créateur, ni titre, ni modèle).
- Saisie correcte → cookie d'accès propre au partage : `Path=/share/<token>`, `HttpOnly`, `Secure`, `SameSite=Lax`, signé HMAC (`SESSION_SECRET`) sur `share_id` + `password_version`, expirant avec le partage.
- Le maillage et l'envoi du choix exigent aussi ce cookie : l'URL du fichier ne contourne pas le mot de passe.
- Anti-force brute : 5 essais / 15 min par IP et par partage (en mémoire) ; après **20 échecs cumulés** (toutes IP), le partage est **verrouillé** (`locked_at`), le créateur est notifié (`share_locked`) ; il révoque le partage et en crée un nouveau si besoin.

### 9.6 Cycle de vie et suppression
- Tâche de fond toutes les 15 min (même modèle que les sauvegardes) : suppression des fichiers puis des lignes des partages expirés.
- Révocation manuelle : suppression immédiate.
- Au démarrage : suppression des fichiers de `SHARE_DIR` sans ligne `share_files` correspondante (orphelins après crash).
- Suppression de compte : fichiers supprimés avec le reste (cascade en base + effacement disque).
- Export de compte : métadonnées des partages actifs et choix reçus, **sans** les fichiers.
- `SHARE_DIR` est **exclu des sauvegardes**.

### 9.7 Routes
| Méthode | Route | Accès |
|---|---|---|
| GET | `/shares`, `/shares/new` | connecté |
| POST | `/shares` (multipart) | connecté |
| POST | `/shares/{id}/password`, `/shares/{id}/revoke` | connecté, propriétaire |
| GET | `/shares/{id}/preview`, `/shares/{id}/file`, `/shares/{id}/thumb` | connecté, propriétaire (aperçu : le lien public n'est plus connu après sa création) |
| GET | `/share/{token}` | public (+ cookie si mot de passe) |
| POST | `/share/{token}/unlock` | public |
| GET | `/share/{token}/files/{fileID}` | public (+ cookie si mot de passe) |
| POST | `/share/{token}/choice` | public (+ cookie si mot de passe) |

Les POST publics restent couverts par `http.CrossOriginProtection` (§10.3).

---

## 10. Architecture technique

### 10.1 Stack
- **Go** (dernière version stable), un seul binaire.
- HTTP : `net/http` (routeur standard Go ≥ 1.22).
- Rendu : `html/template` + **htmx** (servi localement). JS maison minimal : scanner, sélecteur de couleur, liste de réception.
- SQLite : `modernc.org/sqlite` (pur Go, **sans CGO**). Requêtes écrites à la main avec `database/sql` dans le package `store` (sqlc écarté : les filtres dynamiques sont faits en mémoire, le gain ne justifiait pas un outil de génération de plus).
- Interface en français, **vouvoiement**. Dates affichées à l'heure de Paris (`time/tzdata` embarqué : l'image distroless n'a pas de base de fuseaux).
- Migrations SQL versionnées, **embarquées** (`embed`), appliquées automatiquement au démarrage, uniquement ascendantes.
- Templates, CSS, JS, migrations embarqués dans le binaire.
- CSS : feuille maison légère, mobile-first, pas de framework ni d'étape de build front.

### 10.2 Arborescence
```
cmd/filamentmanager/main.go     # config, wiring, démarrage, sous-commande healthcheck
internal/config/                # lecture + validation des env vars
internal/db/                    # ouverture SQLite, migrations embarquées
internal/db/migrations/*.sql
internal/store/                 # accès aux données (users, sessions, produits, bobines…)
internal/auth/                  # jetons, codes, signature, rate limit, IP cliente
internal/catalog/               # référentiels fixes, normalisation des marques
internal/color/                 # conversion Lab, ΔE2000, tri par teinte
internal/stock/                 # validation des saisies, recherche, filtres, tri (logique pure)
internal/mail/                  # interface Mailer + SMTP + Fake (tests)
internal/backup/                # VACUUM INTO périodique + rétention
internal/web/                   # handlers HTTP, middlewares, rendu
internal/web/templates/  internal/web/static/   # embarqués dans le binaire
```
v1b : `internal/store/{friends,transfers,notifications}.go` (règles de visibilité et de transfert appliquées dans les requêtes, en transaction) et `internal/web/{friends,transfer}_handlers.go` + `notify.go` (rédaction des notifications, envoi des emails en arrière-plan avec 3 tentatives).
v1c ajoutera `internal/share/` (lecture / validation / conversion STL, mot de passe, purge) et `internal/web/static/vendor/three/` (Three.js + OrbitControls, modules ES).

### 10.3 Sécurité
- **Cloisonnement** : toute requête de lecture/écriture filtre sur `user_id` de la session ; l'accès au stock d'un tiers passe par une seule fonction de vérification d'amitié + règles §7.2. Couvert par des tests dédiés.
- **CSRF** : cookie `SameSite=Lax` + `http.CrossOriginProtection` de la bibliothèque standard (Go ≥ 1.25), qui rejette les requêtes non sûres cross-origin d'après `Sec-Fetch-Site` / `Origin`. Pas de jeton à propager dans les formulaires. `BASE_URL` est déclarée origine de confiance ; Nginx Proxy Manager doit transmettre l'en-tête `Host` d'origine (comportement par défaut).
- En-têtes : `Content-Security-Policy` stricte (`default-src 'self'`, + origine Plausible si activé), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `Permissions-Policy: camera=(self)`.
- Jetons (session, lien magique, invitation, partage) : 32 octets `crypto/rand`, stockés hashés (SHA-256), comparaison en temps constant.
- Aucune donnée personnelle dans les URL suivies par Plausible. Les pages dont l'URL porte un jeton (`/share/<token>`, `/invite/<token>`, `/auth/verify?t=…`) sont remontées sous une URL générique (`/share/*`, `/invite/*`, `/auth/verify`) via le script manuel de Plausible : le jeton n'en part jamais. La règle est appliquée au rendu, d'après le chemin, pour toute page.
- Export CSV : les cellules commençant par `=`, `+`, `-`, `@` sont préfixées d'une apostrophe (pas d'exécution de formule dans un tableur).
- Pages de partage (v1c) : seule surface publique de l'application, règles spécifiques au §9.

### 10.4 Sauvegardes
- Si `BACKUP_DIR` défini : `VACUUM INTO '<BACKUP_DIR>/filamentmanager-<horodatage>.db'` toutes les `BACKUP_INTERVAL`, conservation des `BACKUP_RETENTION` derniers fichiers.
- `BACKUP_DIR` sur un volume distinct de `/data`, récupéré par la sauvegarde externe quotidienne du serveur.
- `SHARE_DIR` (maillages temporaires des partages) n'est pas sauvegardé.

### 10.5 Observabilité
- Logs JSON sur stdout (`log/slog`), niveau `LOG_LEVEL`. Jamais de jeton ni de code dans les logs.
- `GET /healthz` : processus vivant. `GET /readyz` : base accessible (`SELECT 1`).
- Plausible injecté uniquement si `PLAUSIBLE_URL` et `PLAUSIBLE_DOMAIN` sont définis ; mentionné dans la page confidentialité.

### 10.6 Pages légales
`/confidentialite` : données collectées, finalités, Plausible (sans cookie), durée de conservation, suppression & export en libre-service, contact.
v1c : données saisies par le destinataire d'un partage (prénom et message facultatifs), IP conservée uniquement en mémoire pour la limitation des tentatives, cookie d'accès propre au partage, suppression des modèles 3D et des choix à l'expiration (7 jours).

---

## 11. Configuration (variables d'environnement)

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
| `SHARE_DIR` | Dossier des maillages des partages (v1c), peut être un volume dédié | `/data/shares` |
| `SHARE_MAX_FILE_SIZE` | Taille maximale d'un STL envoyé (v1c) | `50MB` |
| `SHARE_MAX_ACTIVE` | Partages actifs par utilisateur (v1c) | `5` |
| `WEBAUTHN_RP_ID` | Domaine des passkeys (v2) : celui de `BASE_URL` ou un domaine parent. **Le changer rend les passkeys existantes inutilisables.** | domaine de `BASE_URL` |
| `WEBAUTHN_RP_NAME` | Nom affiché par l'appareil lors de l'ajout d'une passkey (v2) | `FilamentManager` |

---

## 12. Build, CI/CD, déploiement

- **Image Docker** multi-étapes : build Go (`CGO_ENABLED=0`) → image finale distroless/static non-root. Volumes : `/data` (base), `/backups` (si `BACKUP_DIR=/backups`), éventuellement un volume dédié pour `SHARE_DIR` (v1c). Port exposé : 8080.
- **CI GitHub Actions** : `go vet`, `go test ./...`, build et push de l'image vers Nexus. *Modèle de workflow et stratégie de tags à fournir par Maxime lors de la mise en place.*
- **Production** : une seule instance (contrainte SQLite). TLS terminé par **Nginx Proxy Manager** sur le serveur ; l'application écoute en HTTP. `TRUSTED_PROXIES` = IP/sous-réseau Docker de NPM.

---

## 13. Tests

- **Unitaires** : ΔE2000 (jeux de référence publiés de Sharma et al.), conversion hexa → Lab, normalisation des marques, machine à états des transferts (pending → accepted/refused/cancelled ; loan → returned/gifted), règles de visibilité, rate limiter.
- **Intégration HTTP** (`httptest` + SQLite temporaire par test + `Mailer` factice qui capture les emails) :
  - inscription / connexion par lien et par code (dont code depuis un autre navigateur = refus, 5 tentatives) ;
  - **cloisonnement** : un utilisateur ne peut lire ni modifier les bobines/produits d'un non-ami, ni les champs privés d'un ami ;
  - invitation (usage unique, expiration, révocation) ;
  - prêt (demande, acceptation partielle, niveau figé, retour) ;
  - don (proposition, demande, conversion de prêt, copie de produit, effacement des champs privés) ;
  - suppression de compte (refus si prêt actif, cascade sinon) ;
  - réception en série (création de N bobines en une transaction).
- **v1c — unitaires** : lecture STL binaire / ASCII, rejets (fichier tronqué, NaN / Inf, 0 triangle, plafond de triangles), recentrage et dimensions, format `FMM1`, hash / vérification PBKDF2, signature et invalidation du cookie d'accès, sélection des couleurs (exclusion vides / masquées / prêtées, regroupement par produit).
- **v1c — intégration HTTP** :
  - création (taille max, `SHARE_MAX_ACTIVE`), révocation et expiration suppriment fichier + lignes, nettoyage des orphelins au démarrage ;
  - **cloisonnement** : actions `/shares/{id}/…` refusées à un autre utilisateur ; la page publique n'expose aucun champ privé (niveau, prix, fournisseur, notes) ;
  - mot de passe : page neutre sans cookie, fichier inaccessible sans cookie, limitation par IP, verrouillage à 20 échecs + notification ;
  - choix : remplacement du précédent, notification + email au créateur, limitation par IP ;
  - suppression de compte : fichiers de partage effacés du disque.
- Pas de tests E2E navigateur en v1.

---

## 14. Points ouverts

- Modèle GitHub Actions et stratégie de tags d'image (à fournir).
- Nom de domaine → `BASE_URL`, `SMTP_FROM`, `PLAUSIBLE_DOMAIN`.
- **Import depuis un dépôt de modèles** (Printables, Thingiverse…, lot ultérieur à v1c) : fournir une URL et laisser l'application récupérer le modèle. À spécifier : API et conditions d'utilisation de chaque site, protection SSRF (liste blanche de domaines, pas d'IP privées, taille et délai bornés), archives zip multi-fichiers (nécessite plusieurs `share_files` par partage).
- **3MF / multicolore** (après v1c) : une couleur par pièce ou par objet.
