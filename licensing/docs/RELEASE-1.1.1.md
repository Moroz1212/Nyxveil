# Nyxveil Control Plane 1.1.1

Emergency deployment-hardening release. The production deploy now verifies the
release payload and database backup, restores that backup into a disposable
database, and rehearses schema v2 migration and validation before stopping the
Control Plane service.

Production migration is validated before binaries are replaced. Any later
failure triggers independently reported binary, configuration, database,
service, and health rollback steps. Database rollback uses the verified backup
through a `master` session.

The existing `NyxveilControlPlane` service, HTTPS port `8443`, and
`cp.nyxveil.ru` deployment architecture are unchanged.
