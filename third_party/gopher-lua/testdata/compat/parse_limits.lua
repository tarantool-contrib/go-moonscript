-- The compiler limits are Lua 5.1's: 200 local variables per function
-- (LUAI_MAXVARS), 60 upvalues (LUAI_MAXUPVALUES), 200 syntax levels
-- (LUAI_MAXCCALLS, from 2 C calls on), fewer than 250 registers
-- (MAXSTACK), whose errors give no token. gopher-lua had 200 registers,
-- which included temporaries (a function with 200 locals Lua 5.1 compiles
-- failed with "register overflow"), 255 upvalues, and errors in its own
-- words. luac reports a register overflow near a token; only whether it
-- is one is printed for those.
local r = string.rep
local function try(name, src)
  local f, err = loadstring(src, "=src")
  if f then
    print(name, "ok")
  elseif err:find("function or expression too complex", 1, true) then
    print(name, "too complex")
  else
    print(name, (err:gsub("^[^:]*:", "")))
  end
end
local function locals(n, init)
  local b = {}
  for i = 0, n - 1 do b[#b + 1] = "local v" .. i .. init .. "\n" end
  return table.concat(b)
end
local function call(n) return "consume(" .. r("1,", n - 1) .. "1)\n" end
local main = "-- main chunk\n"
local nested = "-- nested function\n\n\n\nlocal function oversized()\n"
try("locals main", main .. locals(210, " = 1"))
try("locals nested", nested .. locals(210, " = 1") .. "end\n")
try("uninit main", main .. locals(210, ""))
try("uninit nested", nested .. locals(210, "") .. "end\n")
try("multiline init", main .. locals(200, " = 1") .. "local overflow =\n1\n")
try("local function", main .. locals(200, " = 1") .. "local function overflow()\nend\n")
try("200 locals", main .. locals(200, " = 1") .. "return v199\n")
try("local list", "local " .. r("a, ", 200) .. "a")
try("local list 200", "local " .. r("a, ", 199) .. "a")
try("params", "local function f(" .. r("a, ", 199) .. "a) end")
try("params 201", "local function f(" .. r("a, ", 200) .. "a) end")
try("vararg params", "local function f(" .. r("a, ", 199) .. "...) end")
try("vararg params 201", "local function f(" .. r("a, ", 200) .. "...) end")
try("method params", "local t = {} function t:f(" .. r("a, ", 199) .. "a) end")
try("for locals", locals(197, "") .. "for i = 1, 2 do end")
try("for locals 196", locals(196, "") .. "for i = 1, 2 do end")
try("forin locals", locals(196, "") .. "for k, v in pairs(t) do end")
try("scopes", "do\n" .. locals(200, " = 1") .. "end\ndo\n" .. locals(200, " = 1") .. "end\n")
-- registers
try("call 54", main .. locals(195, " = 1") .. call(54))
try("call 53", main .. locals(195, " = 1") .. call(53))
try("call later", main .. locals(195, " = 1") .. call(20) .. "\n" .. call(60))
try("table", main .. locals(195, " = 1") .. "local t = {" .. r("1,", 300) .. "}\n")
try("table 53", main .. locals(195, " = 1") .. "local t = {" .. r("v1,", 53) .. "}\n")
try("table 54", main .. locals(195, " = 1") .. "local t = {" .. r("v1,", 54) .. "}\n")
try("concat", main .. locals(195, " = 1") .. "local s = " .. r("v1 .. ", 60) .. "v1\n")
try("return", main .. locals(195, " = 1") .. "return " .. r("v1, ", 60) .. "v1\n")
try("assign 54", main .. locals(195, " = 1") .. r("v1, ", 53) .. "v1 = " .. r("v2, ", 53) .. "v2\n")
try("assign 55", main .. locals(195, " = 1") .. r("v1, ", 54) .. "v1 = " .. r("v2, ", 54) .. "v2\n")
try("assign index", "local t, k, v " .. r("t[k], ", 124) .. "t[k] = " .. r("v, ", 124) .. "v")
-- upvalues
for _, n in ipairs{60, 61, 300} do
  local b = {"local function outer()\n"}
  for i = 0, math.floor(n/2) - 1 do b[#b+1] = "local v" .. i .. " = 1\n" end
  b[#b+1] = "return function()\n"
  for i = math.floor(n/2), n - 1 do b[#b+1] = "local v" .. i .. " = 1\n" end
  b[#b+1] = "return function()\nreturn "
  for i = 0, n - 1 do if i > 0 then b[#b+1] = " + " end b[#b+1] = "v" .. i end
  b[#b+1] = "\nend\nend\nend\nreturn outer()()()\n"
  try("upvalues " .. n, table.concat(b))
end
try("upvalues main", locals(61, "") .. "return function() return " .. r("v1 + ", 1) ..
  (function() local b = {} for i = 0, 60 do b[#b+1] = "v" .. i end return table.concat(b, " + ") end)() .. " end")
-- syntax levels
local function levels(name, mk)
  local n = 1
  while n < 400 and loadstring(mk(n), "=src") do n = n + 1 end
  local _, err = loadstring(mk(n), "=src")
  print(name, n - 1, err and (err:gsub("^[^:]*:", "")))
end
levels("do", function(n) return r("do ", n) .. r("end ", n) end)
levels("paren", function(n) return "return " .. r("(", n) .. "1" .. r(")", n) end)
levels("not", function(n) return "return " .. r("not ", n) .. "1" end)
levels("pow", function(n) return "return " .. r("1^", n) .. "1" end)
levels("concat", function(n) return "return " .. r("1 .. ", n) .. "1" end)
levels("table", function(n) return "return " .. r("{", n) .. r("}", n) end)
levels("func", function(n) return r("function f() ", n) .. r("end ", n) end)
levels("funcexpr", function(n) return "return " .. r("function() return ", n) .. "1" .. r(" end", n) end)
levels("if", function(n) return r("if x then ", n) .. r("end ", n) end)
levels("index", function(n) return "return " .. r("a[", n) .. "1" .. r("]", n) end)
levels("assign", function(n) return r("a, ", n) .. "a = 1" end)
levels("nested assign", function(n) return "do do " .. r("a, ", n) .. "a = 1 end end" end)
