FROM postgres:17-alpine

RUN mkdir -p /var/log/postgresql \
    && chown postgres:postgres /var/log/postgresql
