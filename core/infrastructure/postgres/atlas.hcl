env "core" {
  url = getenv("ZUBRIUM_CORE_MIGRATION_DATABASE_URL")

  migration {
    dir = getenv("ZUBRIUM_CORE_MIGRATION_DIR")
  }
}
