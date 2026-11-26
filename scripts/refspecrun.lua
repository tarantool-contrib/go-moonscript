-- usage: lua5.1 scripts/refspecrun.lua <shimdir> <out.json> spec.moon... (see gen-upstream-spec-ref.sh)
-- runs upstream specs with the reference moonscript + the busted stand-in;
-- records every compile.tree call made by the specs
local shim, out = arg[1], arg[2]
local parse = require "moonscript.parse"
local compile = require "moonscript.compile"
local srcs = setmetatable({}, { __mode = "k" })
local records = {}
local recording = false
local pstring = parse.string
parse.string = function(s, ...)
  local tree, err = pstring(s, ...)
  if tree then srcs[tree] = s end
  return tree, err
end
local ctree = compile.tree
compile.tree = function(tree, opts, ...)
  local code, a, b = ctree(tree, opts, ...)
  if recording and srcs[tree] then
    local r = { src = srcs[tree], correlate = opts and opts.correlate and true or false,
      implicitly_return_root = not (opts and opts.implicitly_return_root == false) }
    if code then r.code = code; r.posmap = {}; for k, p in pairs(a) do r.posmap[tostring(k)] = p end else r.err = a; r.pos = b end
    table.insert(records, r)
  end
  return code, a, b
end
package.preload["spec.helpers"] = function() return dofile(shim .. "/helpers.lua") end
local busted = dofile(shim .. "/busted.lua")
local to_lua = require("moonscript.base").to_lua

local function json(v)
  local t = type(v)
  if t == "string" then
    return '"' .. v:gsub('[%c"\\]', function(c)
      return string.format("\\u%04x", c:byte())
    end) .. '"'
  elseif t == "number" or t == "boolean" then return tostring(v)
  elseif t == "table" and #v > 0 then
    local parts = {}
    for _, x in ipairs(v) do table.insert(parts, json(x)) end
    return "[" .. table.concat(parts, ",") .. "]"
  elseif t == "table" then
    local parts = {}
    local keys = {}
    for k in pairs(v) do table.insert(keys, k) end
    table.sort(keys, function(a, b) return tostring(a) < tostring(b) end)
    for _, k in ipairs(keys) do table.insert(parts, json(tostring(k)) .. ":" .. json(v[k])) end
    return "{" .. table.concat(parts, ",") .. "}"
  end
  return "null"
end

local all = {}
for i = 3, #arg do
  local fname = arg[i]
  local f = assert(io.open(fname)); local src = f:read("*a"); f:close()
  local code = assert(to_lua(src))
  local first = busted.count() + 1
  recording = true
  assert(loadstring(code, "@" .. fname))()
  local spec = fname:match("([^/]+)%.moon$")
  for n = first, busted.count() do
    records = {}
    local name, ok, err = busted.run(n)
    print(spec, name, ok, ok ~= true and err or "")
    local list = {}
    for _, r in ipairs(records) do table.insert(list, r) end
    if #list > 0 then table.insert(all, { spec = spec, test = name, compiles = list }) end
  end
  recording = false
end
local o = assert(io.open(out, "w"))
local parts = {}
for _, r in ipairs(all) do
  table.insert(parts, json({ spec = r.spec, test = r.test, compiles = r.compiles }))
end
o:write("[\n" .. table.concat(parts, ",\n") .. "\n]\n")
o:close()
