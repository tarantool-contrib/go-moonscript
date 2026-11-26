-- os.execute, os.remove, os.rename, os.setlocale and os.time as Lua 5.1's
-- over glibc
local function try(f, ...)
  local args, n = {...}, select("#", ...)
  local ok, err = pcall(function() return f(unpack(args, 1, n)) end)
  -- (the quotes of a function name in argument errors are the errors area's)
  return ok, (string.gsub(tostring(err), "to '(%w+)'", "to %1"))
end

-- system(): a shell is there; wait statuses
print(os.execute() ~= 0)
print(os.execute("exit 0"), os.execute("exit 3"), os.execute("exit 255"))
print(os.execute("kill -9 $$"), os.execute("kill -15 $$"))
print(os.execute("nonexistent_command_xyz 2>/dev/null"))
print(try(os.execute, {}))

-- remove and rename: nil, "<name>: <strerror>", errno
local name = os.tmpname()
local function hide(...)
  local t = {...}
  for i = 1, select("#", ...) do
    if type(t[i]) == "string" then t[i] = t[i]:gsub(name, "<tmp>") end
  end
  return unpack(t, 1, select("#", ...))
end
print(name:match("^/tmp/lua_%w%w%w%w%w%w$") ~= nil, io.open(name):read("*a"))
print(hide(os.remove(name .. ".none")))
print(hide(os.rename(name .. ".none", name .. ".other")))
local f = assert(io.open(name, "w")) f:write("x") f:close()
print(hide(os.rename(name, name .. ".2")))
print(hide(os.remove(name)))
print(hide(os.remove(name .. ".2")))
local dir = os.tmpname()
os.remove(dir)
os.execute("mkdir " .. dir .. " && touch " .. dir .. "/f")
print((hide(select(2, os.remove(dir)))):gsub(dir, "<dir>"))
print(os.remove(dir .. "/f"), os.remove(dir), os.remove(dir) == nil)
print(hide(os.rename(name, "/nonexistent/x")))
print((string.gsub(select(2, os.remove("/nonexistent/dir/file")), "^/nonexistent/dir/file", "<name>")))

-- setlocale: only the C locales are there
print(os.setlocale())
print(os.setlocale("C"), os.setlocale("POSIX"), os.setlocale(nil, "numeric"))
print(os.setlocale("no_SUCH.locale"), os.setlocale("en_US.ISO-8859-1", "ctype"))
for _, cat in ipairs({"all", "collate", "ctype", "monetary", "numeric", "time"}) do
  print(cat, os.setlocale(nil, cat), os.setlocale("C", cat))
end
print(os.setlocale("C.UTF-8", "ctype"), os.setlocale(nil, "ctype"), os.setlocale(nil, "time"))
print(os.setlocale())
print(os.setlocale("C.UTF-8"), os.setlocale())
print(os.setlocale("C", "all"), os.setlocale())
print(try(os.setlocale, "C", "bogus") == false)
print(try(os.setlocale, {}))

-- os.time: missing fields, defaults, conversions, normalization, isdst
print(try(os.time, {}))
print(try(os.time, {year = 2000}))
print(try(os.time, {year = 2000, month = 1}))
print(try(os.time, {year = 2000, day = 1}))
print(try(os.time, {month = 1, day = 1}))
print(try(os.time, {year = "x", month = 1, day = 1}))
print(try(os.time, {year = {}, month = 1, day = 1}))
print(try(os.time, 5))
print(try(os.time, "x"))
local t0 = os.time({year = 2000, month = 1, day = 1, hour = 0})
print(os.time({year = 2000, month = 1, day = 1}) - t0)
print(os.time({year = "2000", month = "0x1", day = " 1 ", hour = 0.9}) - t0)
print(os.time({year = 2000, month = 1, day = 1, hour = 0, min = 59, sec = 61}) - t0)
print(os.time({year = 2000, month = 13, day = 1, hour = 0}) - t0)
print(os.time({year = 2000, month = 1, day = 0, hour = -1}) - t0)
print(os.time({year = 2000, month = 1, day = 1, hour = 0, isdst = false}) - t0)
local d = os.date("*t", os.time({year = 2010, month = 7, day = 15, hour = 10, min = 20, sec = 30}))
print(d.year, d.month, d.day, d.hour, d.min, d.sec, d.yday, d.wday)
local mt = setmetatable({}, {__index = function(_, k) return ({year = 2000, month = 1, day = 1, hour = 0})[k] end})
print(os.time(mt) - t0)
print(type(os.time()), type(os.time(nil)), os.time() == math.floor(os.time()))
