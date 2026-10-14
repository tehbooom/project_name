DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'otel_monitor') THEN
        CREATE ROLE otel_monitor LOGIN PASSWORD 'otel_monitor';
    ELSE
        ALTER ROLE otel_monitor WITH LOGIN PASSWORD 'otel_monitor';
    END IF;
END
$$;

GRANT pg_monitor TO otel_monitor;
GRANT CONNECT ON DATABASE project_name TO otel_monitor;
