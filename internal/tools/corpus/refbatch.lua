-- Reference driver for the corpus, run inside moonref:0.8.0 with lua5.1.
-- Embedded into the corpus tool and started by `corpus ref` (see main.go).
--
--   lua5.1 refbatch.lua FILES_DIR OUT_DIR < list
--
-- list: one file name per line (relative to FILES_DIR, e.g. "<sha>.moon").
-- For every input <base>.moon it writes into OUT_DIR:
--   <base>.tree   moonscript.dump.tree output (when parsing succeeded)
--   <base>.lua    compile.tree(tree) output (when compiling succeeded)
--   <base>.err    the error text (parse.string error, or
--                 compile.format_error(msg, pos, src) for compile errors)
--   <base>.status one word: ok | parse-error | compile-error |
--                 compile-internal | parse-internal | dump-internal
--                 (the Go tool adds ref-timeout for files that hung)
-- compile-error = user-error/compile-error thrown by the compiler;
-- compile-internal = a Lua runtime error inside the reference compiler (the
-- message then carries a traceback and is not expected to match verbatim).

local parse = require("moonscript.parse")
local compile = require("moonscript.compile")
local dump = require("moonscript.dump")

local files_dir, out_dir = arg[1], arg[2]
assert(files_dir and out_dir, "usage: refbatch.lua FILES_DIR OUT_DIR < list")

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

-- compile.tree with the internal/user error distinction preserved.
local function compile_tree(tree, src)
  local scope = compile.RootBlock({})
  local runner = coroutine.create(function()
    return scope:root_stms(tree)
  end)
  local ok, err = coroutine.resume(runner)
  if ok then
    return "ok", scope:render()
  end
  -- mirror compile.tree's message construction exactly
  local msg, pos
  local kind = "compile-internal"
  if type(err) == "table" then
    local t = err[1]
    if t == "user-error" or t == "compile-error" then
      msg, pos = unpack(err, 2)
      kind = "compile-error"
    else
      msg = table.concat({
        "Unknown error thrown: " .. tostring(require("moonscript.util").dump(err)),
        debug.traceback(runner)
      }, "\n")
    end
  else
    msg = table.concat({ err, debug.traceback(runner) }, "\n")
  end
  pos = pos or scope.last_pos
  return kind, compile.format_error(msg, pos, src)
end

local n, counts = 0, {}
for name in io.stdin:lines() do
  if name ~= "" then
    local base = out_dir .. "/" .. name:gsub("%.moon$", "")
    -- "pending" marks the file the driver was working on if the run gets
    -- killed (a hang in the reference); the Go side turns it into ref-timeout
    write_file(base .. ".status", "pending\n")
    local src = read_file(files_dir .. "/" .. name)
    local status, tree_out, lua_out, err_out

    local ok, tree, perr = pcall(parse.string, src)
    if not ok then
      status, err_out = "parse-internal", tostring(tree)
    elseif not tree then
      status, err_out = "parse-error", tostring(perr)
    else
      local dok, d = pcall(dump.tree, tree)
      if not dok then
        status, err_out = "dump-internal", tostring(d)
      else
        tree_out = d
        local cok, kind, out = pcall(compile_tree, tree, src)
        if not cok then
          status, err_out = "compile-internal", tostring(kind)
        elseif kind == "ok" then
          status, lua_out = "ok", out
        else
          status, err_out = kind, out
        end
      end
    end

    for _, ext in ipairs({ "tree", "lua", "err" }) do
      os.remove(base .. "." .. ext)
    end
    if tree_out then write_file(base .. ".tree", tree_out) end
    if lua_out then write_file(base .. ".lua", lua_out) end
    if err_out then write_file(base .. ".err", err_out) end
    write_file(base .. ".status", status .. "\n")

    counts[status] = (counts[status] or 0) + 1
    n = n + 1
    if n % 500 == 0 then
      io.stderr:write(n, " files\n")
    end
  end
end

local keys = {}
for k in pairs(counts) do keys[#keys + 1] = k end
table.sort(keys)
for _, k in ipairs(keys) do
  io.stdout:write(k, "\t", counts[k], "\n")
end
io.stdout:write("total\t", n, "\n")
