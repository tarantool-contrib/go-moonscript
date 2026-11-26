-- Runs one file of the official Lua 5.1 test suite (lua5.1-tests) the way
-- its all.lua does, and prints the outcome:
--
--   NOTE	<message>         for each Message() call (a skipped part)
--   RESULT	pass
--   RESULT	fail	<file>:<line>	<message>
--
-- where <file>:<line> is the innermost Lua function on the stack when the
-- error was raised (for a failed assert, the line of the assert).
--
-- Usage: lua run51.lua <file> | --setup, in the suite's directory. The same
-- script runs on our VM, on lua5.1 and on luajit (the reference outcomes).
--
-- Differences from all.lua: every file runs in a fresh interpreter; dofile is
-- not redirected through string.dump/loadstring (binary chunks are not part
-- of what our VM supports); the newproxy __gc that writes dots to stderr is
-- left out; _soft and _port are not set, as in all.lua.

local file = assert(arg[1], "usage: run51.lua <file>")

-- all.lua's setup; "run51.lua --setup" checks it as all.lua does, the
-- files run whether or not it works
local setup = function()
  math.randomseed(0)
  collectgarbage("setstepmul", 180)
  collectgarbage("setpause", 190)
  assert(os.setlocale"C")
end

local print, tostring, type = print, tostring, type
function Message(m)
  print("NOTE\t" .. (string.gsub(string.sub(m, 3, -3), "[\r\n\t]+", " ")))
end

-- what all.lua checks of a file's results
local returns = {
  ["calls.lua"] = function(r) return r == deep and deep end,
  ["attrib.lua"] = function(r) return r == 27 end,
  ["locals.lua"] = function(r) return r == 5 end,
  ["events.lua"] = function(r) return r == 12 end,
  ["verybig.lua"] = function(r) return r == 10 end,
}

local function where(msg)
  local loc = "?"
  for level = 2, 1000 do
    local info = debug.getinfo(level, "Sl")
    if not info then break end
    if info.what ~= "C" and (info.currentline or -1) > 0 then
      loc = info.short_src .. ":" .. info.currentline
      break
    end
  end
  if type(msg) ~= "string" and type(msg) ~= "number" then
    msg = "(error object is a " .. type(msg) .. " value)"
  end
  return loc .. "\t" .. tostring(msg)
end

if file ~= "--setup" then pcall(setup) end

local ok, err = xpcall(function()
  if file == "--setup" then return setup() end
  local f = assert(loadfile(file))
  if file == "big.lua" then
    f = coroutine.wrap(f)
    assert(f() == "b", "big.lua: first yield is not 'b'")
    assert(f() == "a", "big.lua: second yield is not 'a'")
    return
  end
  local r = f()
  if returns[file] and not returns[file](r) then
    error("all.lua: unexpected result of " .. file .. ": " .. tostring(r), 0)
  end
end, where)

if ok then
  print("RESULT\tpass")
else
  print("RESULT\tfail\t" .. (string.gsub(err, "[\r\n]", "\\n")))
end
