# Atlas configuration for the website engine.
#
# The schema is defined by the Go models in internal/models. The loader
# program renders them as DDL, Atlas compares that DDL with the state of the
# database described by the migration directory and writes the difference as
# a new SQL migration file.
#
#   make migrate-diff   # generate a migration from the models
#   make migrate-apply  # apply the migrations to DATABASE_URL

env "local" {
  src = "file://migrations"
  dev = "docker://postgres/16/dev"

  migration {
    format = sql
    diff   = "exec:go run ./cmd/atlas-loader"
  }
}
