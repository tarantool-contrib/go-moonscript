-- yuin/gopher-lua#517: field separators in table constructors
for _, src in ipairs{"{1,2},,{2,3}", "x = {{1,2},,{2,3}}", "x = {1,,2}", "x = {,}",
    "x = {;}", "x = {1;;2}", "x = {1,;2}", "x = {1,}", "x = {1;}", "x = {a = 1,}",
    "x = {1, a = 2; [3] = 3,}", "x = {1,2,,}", "f(1,,2)"} do
  local f = loadstring(src)
  print(src, f and "ok" or "error")
end
local t = {1, 2; 3, a = 4,}
print(#t, t.a)
