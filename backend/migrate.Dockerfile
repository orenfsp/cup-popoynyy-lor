FROM migrate/migrate:v4.18.1
COPY backend/migrations /migrations
ENTRYPOINT ["migrate", "-path", "/migrations", "-database"]
