-- Aléa du lien de partage : le jeton en est dérivé par HMAC (SESSION_SECRET), ce qui permet au
-- créateur de retrouver le lien sans le stocker en clair. NULL pour les partages antérieurs.
ALTER TABLE shares ADD COLUMN link_nonce TEXT;
