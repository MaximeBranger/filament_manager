# FilamentManager

Application web ultra légère pour suivre son stock de bobines de filament 3D.

- **Stock** : une ligne par bobine, niveau de remplissage, recherche texte, filtres, recherche par couleur proche (ΔE2000).
- **Scan** des codes-barres des boîtes, à l'unité ou en série à la réception.
- **Amis** : consultation du stock des amis, prêts et dons de bobines, notifications.
- **Partage de modèles 3D** : un lien temporaire montre un STL dans les couleurs de votre stock, le destinataire choisit la sienne.
- **Connexion** sans mot de passe (lien ou code par email, passkeys). Application installable (PWA).

La spécification complète est dans [SPEC.md](SPEC.md).

## Stack

Go (bibliothèque standard, `modernc.org/sqlite`, `go-webauthn`), rendu serveur avec `html/template` et htmx, SQLite, Three.js pour l'aperçu 3D. Un seul binaire statique, une image Docker d'environ 21 Mo.

## Développement

Prérequis : Go (version indiquée dans `go.mod`) et Docker pour le serveur SMTP de test.

```sh
# Serveur SMTP de test : les emails sont visibles sur http://localhost:8025
docker run -d --rm --name mailpit -p 1025:1025 -p 8025:8025 axllent/mailpit

# Lancer l'application
BASE_URL=http://localhost:8080 \
SESSION_SECRET=dev-secret-dev-secret-dev-secret-00 \
SMTP_HOST=localhost SMTP_PORT=1025 SMTP_TLS=none \
SMTP_FROM='FilamentManager <noreply@localhost>' \
DATABASE_PATH=./dev.db \
go run ./cmd/filamentmanager
```

Le scan par caméra exige HTTPS, sauf sur `localhost`. Pour tester depuis un téléphone, passez par un tunnel HTTPS ou par le reverse proxy.

```sh
go test ./...          # tous les tests
go test -race ./...    # comme la CI
```

## Configuration

Tout passe par des variables d'environnement. Le tableau complet se trouve dans [SPEC.md §11](SPEC.md#11-configuration-variables-denvironnement). Les variables obligatoires sont `BASE_URL`, `SESSION_SECRET` (au moins 32 caractères), `SMTP_HOST` et `SMTP_FROM`. L'application refuse de démarrer si l'une d'elles manque.

## Production

```sh
docker run -d --name filament-manager \
  -p 8080:8080 \
  -v filament-data:/data \
  -v filament-backups:/backups \
  -v filament-shares:/shares \
  -e BASE_URL=https://filaments.example.fr \
  -e SESSION_SECRET=... \
  -e SMTP_HOST=... -e SMTP_USER=... -e SMTP_PASSWORD=... \
  -e SMTP_FROM='FilamentManager <noreply@example.fr>' \
  -e BACKUP_DIR=/backups \
  -e SHARE_DIR=/shares \
  -e TRUSTED_PROXIES=172.18.0.0/16 \
  <nexus>/<repo>/filament-manager:<tag>
```

- **Une seule instance** à la fois : c'est une contrainte de SQLite. Les défis de passkeys et les limites de tentatives sont aussi gardés en mémoire.
- **TLS** : il est terminé par Nginx Proxy Manager. `TRUSTED_PROXIES` doit contenir son IP ou son sous-réseau Docker.
- **Taille des envois** : NPM limite le corps des requêtes à environ 1 Mo par défaut. Pour envoyer des STL, ajoutez dans la configuration avancée du proxy host `client_max_body_size 51m;` (au-dessus de `SHARE_MAX_FILE_SIZE`).
- **Sauvegardes** : une copie cohérente de la base est écrite dans `/backups` au démarrage, puis toutes les `BACKUP_INTERVAL`. C'est ce dossier que la sauvegarde externe doit récupérer.
- **Modèles 3D partagés** : `SHARE_DIR` sur un volume dédié, à exclure des sauvegardes (les partages ne vivent que 7 jours).
- **Import depuis un lien** : Printables et les liens directs fonctionnent sans configuration. Pour Thingiverse, créez une application sur thingiverse.com/developers et passez son jeton dans `THINGIVERSE_TOKEN`. Le conteneur doit pouvoir sortir en HTTPS vers Internet.
- **Passkeys** : elles sont liées au domaine (`WEBAUTHN_RP_ID`, par défaut celui de `BASE_URL`). Changer de domaine les rend inutilisables ; le lien par email reste disponible.
- **Santé** : `/healthz` (processus) et `/readyz` (base de données). L'image embarque un `HEALTHCHECK`.

## Référence des codes-barres

Les codes-barres inconnus de votre stock sont recherchés dans un index embarqué, tiré de l'[Open Filament Database](https://github.com/OpenFilamentCollective/open-filament-database) (licence MIT, environ 6 000 codes). Aucun appel réseau n'a lieu à l'exécution.

- Régénérer l'index à la main :
  ```sh
  gh release download -R OpenFilamentCollective/open-filament-database -p all.json.gz \
    "$(gh release list -R OpenFilamentCollective/open-filament-database --limit 1 --json tagName --jq '.[0].tagName')"
  go run ./cmd/ofdimport -in all.json.gz -out internal/reference/ofd.json
  ```
- Mise à jour automatique : le workflow [reference-refresh.yml](.github/workflows/reference-refresh.yml) ouvre chaque lundi une pull request quand la base a changé. Il faut activer dans le dépôt *Settings → Actions → General → « Allow GitHub Actions to create and approve pull requests »*.

## CI

Le workflow [.github/workflows/ci.yml](.github/workflows/ci.yml) enchaîne `gofmt`, `go vet`, les tests avec le détecteur de concurrence, le build de l'image, un scan Trivy et le push vers Nexus.

Il attend les secrets `NEXUS_USERNAME` et `NEXUS_PASSWORD`, ainsi que les variables `NEXUS_URL` et `NEXUS_REPOSITORY`.

Tags d'image :

| Déclencheur | Tags poussés |
|---|---|
| Tag git `vX.Y.Z` | `vX.Y.Z` et `latest` |
| Tag git `*-beta*` | le tag et `beta` |
| Push sur `main` | `snapshot` |
| Push sur une autre branche | `dev-snapshot` |
