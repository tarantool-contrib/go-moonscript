return {
  whitelist_globals = {
    ["."] = { "everywhere" },
    ["^app/"] = { "ngx", "app_global" },
    ["spec%.moon$"] = { "describe", "it" },
  }
}
