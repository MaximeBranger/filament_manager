# FilamentManager

Application web ultra légère pour suivre son stock de bobines de filament 3D : liste, recherche par couleur, scan des codes-barres, et bientôt partage entre amis.

La spécification complète est dans [SPEC.md](SPEC.md).

## Stack

Go (bibliothèque standard + `modernc.org/sqlite`), rendu serveur avec `html/template` et htmx, SQLite. Un seul binaire statique, une image Docker d'environ 16 Mo.

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
  -e BASE_URL=https://filaments.example.fr \
  -e SESSION_SECRET=... \
  -e SMTP_HOST=... -e SMTP_USER=... -e SMTP_PASSWORD=... \
  -e SMTP_FROM='FilamentManager <noreply@example.fr>' \
  -e BACKUP_DIR=/backups \
  -e TRUSTED_PROXIES=172.18.0.0/16 \
  <nexus>/<repo>/filament-manager:<tag>
```

- **Une seule instance** à la fois : c'est une contrainte de SQLite.
- **TLS** : il est terminé par Nginx Proxy Manager. `TRUSTED_PROXIES` doit contenir son IP ou son sous-réseau Docker.
- **Sauvegardes** : une copie cohérente de la base est écrite dans `/backups` au démarrage, puis toutes les `BACKUP_INTERVAL`. C'est ce dossier que la sauvegarde externe doit récupérer.
- **Santé** : `/healthz` (processus) et `/readyz` (base de données). L'image embarque un `HEALTHCHECK`.

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
