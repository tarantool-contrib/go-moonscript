-- A runner for LuaJIT-tests (test/LuaJIT-tests of tarantool/luajit), derived
-- from its test.lua (Copyright (C) 2005-2017 Mike Pall, MIT license; see
-- COPYRIGHT in luajit-tests.tar.gz). Differences from test.lua:
--
--   - the tags are fixed (see below) instead of detected, so that every
--     interpreter (our VM, lua5.1, luajit) runs the same tests;
--   - one file per process, a file that does not compile is reported
--     instead of aborting the run, and the results are machine readable.
--
-- Usage, in the LuaJIT-tests directory:
--
--   lua ljrun.lua --list     prints "FILE\t<path>" for every test file the
--                            index files select, "SKIP\t<path>\t<meta>" for
--                            the entries the tags exclude
--   lua ljrun.lua <path>     runs the tests of a file, printing
--                            "TEST\t<n>\t<name>\tok", "TEST\t<n>\t<name>\tskip"
--                            or "TEST\t<n>\t<name>\tfail\t<first error line>",
--                            or "LOAD\tfail\t<message>"

local assert, io_open, io_lines, load, type, xpcall =
      assert, io.open, io.lines, load, type, xpcall
local tostring, loadstring = tostring, loadstring or load
local print = print

-- The tags: Lua 5.1 with the LuaJIT 2.1 language and library extensions
-- (goto, hex floats, bit, table.new) on x64 Linux, without the ffi and the
-- JIT compiler, and without the slow tests.
local tags = {
  lua = 5.1, luajit = 2.1,
  ["goto"] = true, hexfloat = true, bit = true, ["table.new"] = true,
  x64 = true, linux = true, le = true, abi64 = true,
}

local function want_meta(meta)
  for polarity, tag, cond in meta:gmatch"([+-])([^ <>=]+)([<>=0-9.]*)" do
    local tagval = tags[tag]
    local condresult
    if cond == "" or not tagval then
      condresult = tagval
    else
      condresult = assert(loadstring("return (...) ".. cond))(tagval)
    end
    if polarity == "-" then
      condresult = not condresult
    end
    if not condresult then
      return false
    end
  end
  return true
end

local function list(path)
  for line in io_lines(path .."index") do
    if line ~= "" then
      local metaidx = line:find" +[-+@]"
      local name, meta = line, ""
      if metaidx then
        name, meta = line:sub(1, metaidx - 1), line:sub(metaidx)
      end
      if not want_meta(meta) then
        print("SKIP\t".. path .. name .."\t".. (meta:gsub("^ +", "")))
      elseif name:sub(-4) == ".lua" then
        print("FILE\t".. path .. name)
      else
        list(path .. name .."/")
      end
    end
  end
end

local function scan(path)
  local f = assert(io_open(path, "rb"))
  local contents = f:read"*a"
  f:close()
  local prefix = "return {"
  local code = contents:gsub("()(do +%-%-%- +)([^\r\n]+)",
    function(pos, marker, info)
      if pos ~= 1 then
        pos = pos - 1
        if contents:sub(pos, pos) ~= "\n" then
          return marker .. info
        end
      end
      local result = ("%s%q,function()"):format(prefix, info)
      prefix = ","
      if info:find" !lex" and not want_meta(info:sub((info:find" +[-+@!]"))) then
        result = result .."end--[========["
        prefix = "]========]".. prefix
      end
      return result
    end)
  if prefix:sub(-1) ~= "," then
    error("No tests found in ".. path, 0)
  end
  prefix = prefix .."}"
  local fn, err = load(function()
    local result = code
    code = code ~= prefix and prefix or nil
    return result
  end, "@".. path)
  if not fn then error(err, 0) end
  return fn()
end

local function seal_globals()
  local sealed_mt = {__newindex = function()
    error("Tests should not mutate global state", 3)
  end}
  local function seal(t)
    if getmetatable(t) then return end
    setmetatable(t, sealed_mt)
    for _, v in pairs(t) do
      if type(v) == "table" then seal(v) end
    end
  end
  seal(_G)
  if getmetatable(package.loaded) == sealed_mt then
    setmetatable(package.loaded, nil)
  end
end

local function firstline(err)
  return (tostring(err):match"^[^\n]*")
end

local path = assert(arg[1], "usage: ljrun.lua --list | <file>")
if path == "--list" then
  list("")
  return
end

package.path = "./?.lua;".. package.path
seal_globals()
assert(require"common.test_runner_canary" == "canary is alive")

local ok, tree = pcall(scan, path)
if not ok then
  print("LOAD\tfail\t".. firstline(tree))
  return
end
for i = 1, #tree, 2 do
  local info, test = tree[i], tree[i+1]
  local n = (i + 1) / 2
  local name, want = info, true
  local metaidx = info:find" +[-+@!]"
  if metaidx then
    name = info:sub(1, metaidx - 1)
    want = want_meta(info:sub(metaidx))
  end
  if want and info:find"!private_G" then
    local G = setmetatable({}, {__index = _G})
    G._G = G
    setfenv(test, G)
  end
  if not want then
    print("TEST\t".. n .."\t".. name .."\tskip")
  else
    local tok, err = xpcall(test, debug.traceback)
    if tok then
      print("TEST\t".. n .."\t".. name .."\tok")
    else
      print("TEST\t".. n .."\t".. name .."\tfail\t".. firstline(err))
    end
  end
end
