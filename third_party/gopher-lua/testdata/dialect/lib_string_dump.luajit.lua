-- string.dump under DialectLuaJIT: LuaJIT's signature "\27LJ" (with a
-- version byte of the copy's own), string.dump(f, true) strips the names
-- (output recorded from luajit 2.1 in ref/)
local function add(a, b) local s = a + b return s end
local d, d2 = string.dump(add), string.dump(add, true)
print(d:sub(1, 3) == "\27LJ", d2:sub(1, 3) == "\27LJ", #d2 < #d)
print(loadstring(d)(1, 2), loadstring(d2)(3, 4))
print(debug.getlocal(loadstring(d), 1), debug.getlocal(loadstring(d2), 1))
print(loadstring("\27LJ"))
print((string.gsub(select(2, pcall(string.dump, print)), "^.-:%d+: ", "")))
