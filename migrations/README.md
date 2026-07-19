# Migrations

We'll use plain numbered SQL files with `golang-migrate` (added in Phase 2),
not an ORM's auto-migration — auto-migration is convenient in a tutorial and
dangerous in production: it can silently generate a destructive schema
change (e.g. drop-and-recreate a column) that you never explicitly reviewed.
Writing migrations by hand means every schema change is a reviewable diff.

Naming convention (golang-migrate expects this):

    000001_create_users_table.up.sql
    000001_create_users_table.down.sql
    000002_create_monitors_table.up.sql
    000002_create_monitors_table.down.sql
    ...

Phase 2 will fill this directory in with the real schema:
users, monitors, checks, incidents, alerts.
