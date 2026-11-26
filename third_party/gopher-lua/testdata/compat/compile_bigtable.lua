-- yuin/gopher-lua#537: table constructors with more than 25550 positional
-- items (SETLIST with C = 0, the page in the next word)
local n = 25620 -- not a multiple of 50 (see compile_tablector)
local chunks = {}
for c = 0, n / 1000 do -- (gopher-lua's table.concat fails on long lists)
  local parts = {}
  for i = c * 1000 + 1, math.min(n, c * 1000 + 1000) do
    parts[#parts + 1] = (i % 1000 == 0 or i == 1 or i >= 25549) and tostring(i) or "x"
  end
  chunks[#chunks + 1] = table.concat(parts, ",")
end
local list = table.concat(chunks, ",")
local function run(src, ...)
  return assert(loadstring("local x = 0 " .. src))(...)
end
local t = run("return {" .. list .. "}")
print(#t, t[1], t[2], t[25000], t[25550], t[25551], t[n])
local r = assert(loadstring("return { outer = { arr = {" .. string.gsub(list, "x", "0") .. "} } }"))()
print(r.outer and #r.outer.arr)
print(run("local a, b = ... local y = {" .. list .. "} local p, q = a, b local z = {" .. list .. "} return #y, p, q, #z", 5, 6))
print(#run("return false or {" .. list .. "}"))
local m = run("return {" .. list .. ", ...}", 7, 8)
print(#m, m[n], m[n + 1], m[n + 2])
m = run("return {" .. list .. ", k = 1, ...}", 7, 8)
print(#m, m[n], m[n + 1], m[n + 2], m.k)
