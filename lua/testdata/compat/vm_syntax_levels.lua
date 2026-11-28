-- Deeply nested code is a compile error ("chunk has too many syntax
-- levels"), as in Lua 5.1 (whose limit is 200 levels; gopher-lua's is far
-- higher); gopher-lua's compiler overflowed the Go stack, a fatal error.
-- Long left-associative chains, which Lua 5.1 accepts at any length,
-- still compile (and a chain of constants is no longer folded in
-- quadratic time).
local r = string.rep
local function try(name, src)
  local f, err = loadstring(src, "=x")
  if f then
    print(name, "ok", f())
  else
    print(name, string.find(err, "chunk has too many syntax levels", 1, true) ~= nil)
  end
end
try("unary", "return " .. r("- ", 150000) .. "1")
try("not", "return " .. r("not ", 150000) .. "1")
try("pow", "return " .. r("1 ^ ", 150000) .. "1")
try("blocks", r("do ", 150000) .. r("end ", 150000))
try("index 30000", "local t = {} t.a = t return t" .. r(".a", 30000) .. " == t")
try("sum 30000", "local a = 1 return " .. r("a + ", 30000) .. "a")
try("constant sum 30000", "return " .. r("1 + ", 30000) .. "1")
try("calls 30000", "local t = {} function t.f() return t end return t" .. r(".f()", 30000) .. " == t")
