-- Stand-in for upstream spec/helpers.moon. unindent is the same algorithm;
-- with_dev just runs its function in a setup block: the "dev" MoonScript
-- modules are always the ones the Go test harness preloads (Go-backed
-- moonscript.parse / moonscript.compile, plus upstream Lua-level modules
-- compiled by the Go port).
local function unindent(str)
  local indent = str:match("^%s+")
  if not indent then return str end
  return (str:gsub("\n" .. indent, "\n"):gsub("%s+$", ""):gsub("^%s+", ""))
end

local function with_dev(fn)
  if fn then setup(fn) end
  return package.loaded
end

return { unindent = unindent, with_dev = with_dev }
