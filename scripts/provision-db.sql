-- Creates KM's role, database and schema on the shared PostgreSQL.
-- Run ONCE, as superuser. Not idempotent on purpose: if the role or the
-- database already exist it fails instead of overwriting them.
--
--   docker exec -i atm-postgres psql -v ON_ERROR_STOP=1 -U postgres \
--     -v app_password="'<password>'" -d postgres < scripts/provision-db.sql

CREATE ROLE km_app WITH LOGIN PASSWORD :app_password;
CREATE DATABASE km OWNER km_app;

REVOKE CONNECT ON DATABASE km FROM PUBLIC;
GRANT CONNECT ON DATABASE km TO km_app;

\connect km

CREATE SCHEMA IF NOT EXISTS km AUTHORIZATION km_app;

ALTER SCHEMA public OWNER TO postgres;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM km_app;

ALTER ROLE km_app IN DATABASE km SET search_path TO km;
