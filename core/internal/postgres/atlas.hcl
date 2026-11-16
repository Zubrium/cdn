env "core" {
  url = getenv("ZUBRIUM_CORE_MIGRATION_DATABASE_URL")

  migration {
    dir = getenv("ZUBRIUM_CORE_MIGRATION_DIR")
  }
}

env "core_schema" {
  url = getenv("ZUBRIUM_CORE_DATABASE_URL")

  migration {
    dir = getenv("ZUBRIUM_CORE_SCHEMA_MIGRATION_DIR")
  }
}
