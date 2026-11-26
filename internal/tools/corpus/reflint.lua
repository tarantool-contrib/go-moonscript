-- Reference lint driver for the corpus, run inside moonref:0.8.0 with lua5.1.
-- Embedded into the corpus tool and started by `corpus ref -lint`.
--
--   lua5.1 reflint.lua FILES_DIR OUT_DIR < list
--
-- list: one file name per line (relative to FILES_DIR, e.g. "<id>.moon").
-- For every input <id>.moon it does what lint_code(src, "<id>.moon") does
-- (all stages, default whitelist), but renders the errors with both
-- formatters from a single compile, and writes into OUT_DIR:
--   <id>.status   one word: clean | lint | parse-error | crash-table |
--                 crash-string (lint_code raised; moonc would die)
--   <id>.default  the default format report (status lint)
--   <id>.compact  the compact format report (status lint)
--   <id>.err      the parse error, or the crash: for crash-table the
--                 error object's fields, one per line; for crash-string
--                 the message (it carries reference file:line prefixes)

local lint = require("moonscript.cmd.lint")
local parse = require("moonscript.parse")

local function upvalue(fn, want)
  local i = 1
  while true do
    local name, value = debug.getupvalue(fn, i)
    if not name then
      error("no upvalue " .. want)
    end
    if name == want then
      return value
    end
    i = i + 1
  end
end

local LinterBlock = upvalue(lint.lint_code, "LinterBlock")
local formatters = upvalue(lint.lint_code, "formatters")

local files_dir, out_dir = arg[1], arg[2]
assert(files_dir and out_dir, "usage: reflint.lua FILES_DIR OUT_DIR < list")

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

-- lint_code's body, keeping the errors for both formatters
local function lint_both(code, name)
  local tree, err = parse.string(code)
  if not tree then
    return "parse-error", nil, nil, err
  end
  local scope = LinterBlock(nil, nil)
  scope:stms(tree)
  scope:lint_check_unused()
  if not next(scope.lint_errors) then
    return "clean"
  end
  return "lint", formatters.default(scope.lint_errors, code, name),
    formatters.compact(scope.lint_errors, code, name)
end

local n, counts = 0, {}
for name in io.stdin:lines() do
  if name ~= "" then
    local base = out_dir .. "/" .. name:gsub("%.moon$", "")
    write_file(base .. ".status", "pending\n")
    local src = read_file(files_dir .. "/" .. name)

    local ok, status, default, compact, err = pcall(lint_both, src, name)
    if not ok then
      local e = status
      if type(e) == "table" then
        local parts = {}
        for i = 1, 3 do
          parts[i] = tostring(e[i])
        end
        status, err = "crash-table", table.concat(parts, "\n")
      else
        status, err = "crash-string", tostring(e)
      end
    end

    for _, ext in ipairs({ "default", "compact", "err" }) do
      os.remove(base .. "." .. ext)
    end
    if default then write_file(base .. ".default", default) end
    if compact then write_file(base .. ".compact", compact) end
    if err then write_file(base .. ".err", err) end
    write_file(base .. ".status", status .. "\n")

    counts[status] = (counts[status] or 0) + 1
    n = n + 1
    if n % 1000 == 0 then
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
