-- The car's and the borrower's names are gone: nobody uses them. The data is
-- dropped with the columns.

-- +goose Up
ALTER TABLE user_settings
    DROP COLUMN car_name,
    DROP COLUMN borrower_name;

-- +goose Down
ALTER TABLE user_settings
    ADD COLUMN car_name      VARCHAR(60) NOT NULL DEFAULT 'Mi auto',
    ADD COLUMN borrower_name VARCHAR(40) NOT NULL DEFAULT 'Familiar';
