-- smplkit Local Platform — CI database bootstrap
--
-- Postgres runs `*.sql` files in /docker-entrypoint-initdb.d on first
-- start. Each smplkit service owns its own database; the dev setup
-- pulls them into existence via the app repo's sync script, but CI
-- runs a fresh container per job and needs the databases there from
-- the first connection.

CREATE DATABASE smplkit_app;
CREATE DATABASE smplkit_config;
CREATE DATABASE smplkit_flags;
CREATE DATABASE smplkit_logging;
CREATE DATABASE smplkit_audit;
