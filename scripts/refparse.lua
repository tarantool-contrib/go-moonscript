-- Reference parse driver, run inside the moonref:0.8.0 container with
-- lua5.1. It produces exactly what `moonc -T` prints for a file: the
-- moonscript.dump.tree output on success, or the parse.string error message
-- on failure.
--
-- Batch mode (golden generation):
--   lua5.1 refparse.lua batch file1.moon [file2.moon ...]
--   writes file.tree (tree + "\n") or file.err (message + "\n") next to each
--   input and removes the other one if present.
--
-- Server mode (differential fuzzing):
--   lua5.1 refparse.lua serve
--   reads requests "<len>\n<bytes>" from stdin, answers "T <len>\n<bytes>"
--   (tree) or "E <len>\n<bytes>" (error message) on stdout.
--
-- Trace mode (differential fuzzing of the furthest-failure bookkeeping):
--   lua5.1 refparse.lua serve-trace
--   same protocol, but parses with the pure-Lua pgen parser (slow.lua, same
--   generated grammar as the native module) and answers "T <len>\n<list>"
--   where list is the space separated sequence of 0-based positions at which
--   the furthest failure position increased during the parse.

local parse = require("moonscript.parse")
local dump = require("moonscript.dump")

local function run(src)
  local ok, tree, err = pcall(parse.string, src)
  if not ok then
    return "E", "lua error: " .. tostring(tree)
  end
  if not tree then
    return "E", tostring(err)
  end
  local ok2, out = pcall(dump.tree, tree)
  if not ok2 then
    return "E", "dump error: " .. tostring(out)
  end
  return "T", out
end

local function read_file(path)
  local f = assert(io.open(path, "rb"))
  local s = f:read("*a")
  f:close()
  return s
end

local function write_file(path, s)
  local f = assert(io.open(path, "wb"))
  f:write(s)
  f:close()
end

local function load_traced_slow()
  local path
  for dir in package.path:gmatch("[^;]+") do
    local p = dir:gsub("%?", "moonscript/parse/slow")
    local f = io.open(p, "rb")
    if f then f:close() path = p break end
  end
  local src = read_file(assert(path, "slow.lua not found"))
  local n
  src, n = src:gsub("local function record_furthest%(parser%)\n  if parser.pos > parser.furthest_fail then\n",
    "local function record_furthest(parser)\n  if parser.pos > parser.furthest_fail then\n    PGEN_TRACE[#PGEN_TRACE + 1] = parser.pos\n", 1)
  assert(n == 1, "could not patch record_furthest")
  return assert(loadstring(src, "=slow.lua"))()
end

local mode = arg[1]
if mode == "batch" then
  for i = 2, #arg do
    local path = arg[i]
    local base = path:gsub("%.moon$", "")
    local kind, out = run(read_file(path))
    if kind == "T" then
      write_file(base .. ".tree", out .. "\n")
      os.remove(base .. ".err")
    else
      write_file(base .. ".err", out .. "\n")
      os.remove(base .. ".tree")
    end
  end
elseif mode == "serve" then
  io.stdout:setvbuf("full")
  while true do
    local header = io.stdin:read("*l")
    if not header then break end
    local n = tonumber(header)
    local src = n > 0 and io.stdin:read(n) or ""
    local kind, out = run(src)
    io.stdout:write(kind, " ", #out, "\n", out)
    io.stdout:flush()
  end
elseif mode == "serve-trace" then
  local slow = load_traced_slow()
  io.stdout:setvbuf("full")
  while true do
    local header = io.stdin:read("*l")
    if not header then break end
    local n = tonumber(header)
    local src = n > 0 and io.stdin:read(n) or ""
    PGEN_TRACE = {}
    pcall(slow.parse, src)
    local out = table.concat(PGEN_TRACE, " ")
    io.stdout:write("T ", #out, "\n", out)
    io.stdout:flush()
  end
else
  io.stderr:write("usage: refparse.lua batch FILES... | serve | serve-trace\n")
  os.exit(2)
end
