-- Local machine only: a separate database for the integration tests, so they
-- never touch the data you click around with. Requires provision-db.sql first.
--
--   docker exec -i atmosfera-postgres psql -v ON_ERROR_STOP=1 -U postgres \
--     -d postgres < scripts/provision-test-db.sql

CREATE DATABASE km_test OWNER km_app;

REVOKE CONNECT ON DATABASE km_test FROM PUBLIC;
GRANT CONNECT ON DATABASE km_test TO km_app;

\connect km_test

CREATE SCHEMA IF NOT EXISTS km AUTHORIZATION km_app;

ALTER SCHEMA public OWNER TO postgres;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM km_app;

ALTER ROLE km_app IN DATABASE km_test SET search_path TO km;
