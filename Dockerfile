# syntax=docker/dockerfile:1

# Version de correctif épinglée (comme la directive toolchain de go.mod) : l'image officielle
# impose GOTOOLCHAIN=local, un tag flottant donnerait la stdlib présente au moment du build.
FROM golang:1.26.10-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/filamentmanager ./cmd/filamentmanager \
 && mkdir -p /out/data /out/backups /out/shares

# Image finale : binaire statique seul, sans shell, utilisateur non root (uid 65532).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/filamentmanager /filamentmanager
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build --chown=65532:65532 /out/backups /backups
# Point de montage facultatif pour SHARE_DIR (volume dédié, hors sauvegardes).
COPY --from=build --chown=65532:65532 /out/shares /shares
ENV LISTEN_ADDR=:8080 \
    DATABASE_PATH=/data/app.db
VOLUME ["/data", "/backups"]
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/filamentmanager", "healthcheck"]
ENTRYPOINT ["/filamentmanager"]
