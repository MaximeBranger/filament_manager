-- Provenance d'un modèle importé depuis un lien (attribution exigée par les licences CC BY).
ALTER TABLE share_files ADD COLUMN source_site    TEXT;
ALTER TABLE share_files ADD COLUMN source_url     TEXT;
ALTER TABLE share_files ADD COLUMN source_title   TEXT;
ALTER TABLE share_files ADD COLUMN source_author  TEXT;
ALTER TABLE share_files ADD COLUMN source_license TEXT;
