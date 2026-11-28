-- go-moonscript: differential fuzz test of tables (table.go,
-- table_hash.go). Random sequences of sets, nil sets, #, gets, pairs and
-- next traversals that assign to the current key, table.insert,
-- table.remove, ipairs, unpack and table.maxn; everything is printed but
-- the iteration order (a traversal prints its pairs sorted), so that the
-- output can be compared with Lua 5.1's (testdata/compat/ref, recorded by
-- scripts/gen-compat-ref.sh). One line per sequence. The environment
-- variables FUZZ_SEED, FUZZ_N and FUZZ_OPS select other sequences
-- (scripts/fuzz-tables.sh runs them against Lua 5.1).
local function env(name, default) return tonumber(os.getenv(name) or "") or default end
local FUZZ_SEED, FUZZ_N, FUZZ_OPS = env("FUZZ_SEED", 1), env("FUZZ_N", 300), env("FUZZ_OPS", 60)

local seed
local function rand(n) -- 1..n (Park-Miller, exact in doubles)
  seed = seed * 16807 % 2147483647
  return seed % n + 1
end

local strs = {"a", "b", "c", "d", "e", "f", "g", "h", "n", "x1", "x2", "x3"}
local nextval = 0

local function repr(v)
  if type(v) == "string" then return "'" .. v end
  return tostring(v)
end

local function key(t, size)
  local r = rand(20)
  if r <= 9 then return rand(size) end
  if r == 10 then return #t + 1 end
  if r == 11 then return #t end
  if r == 12 then return #t + rand(3) + 1 end
  if r == 13 then return 2 ^ rand(12) + rand(3) - 2 end
  if r == 14 then return rand(3) - 3 end -- -2, -1, 0
  if r == 15 then return rand(size) + 0.5 end
  if r == 16 then return rand(3) == 1 end
  return strs[rand(#strs)]
end

local function dump(t)
  local r = {}
  for k, v in pairs(t) do r[#r + 1] = repr(k) .. "=" .. repr(v) end
  table.sort(r)
  return table.concat(r, ",")
end

local function newtable(size)
  local r = rand(8)
  if r == 1 then return {} end
  if r == 2 then return {1, 2, 3, nil, 5} end
  if r == 3 then return {nil, nil, 3} end
  if r == 4 then return {1, 2, 3, a = 1, [10] = 10} end
  if r == 5 then return {x1 = 1, x2 = 2, x3 = 3, [1.5] = 1} end
  if r == 6 then
    local t = {}
    for i = 1, rand(size) do t[i] = i end
    return t
  end
  if r == 7 then
    local t = {}
    for i = rand(size), 1, -1 do t[i] = i end
    return t
  end
  local function f(...) return {...} end
  return f(1, nil, 3, nil, nil, 6, rand(2) == 1 and 7 or nil)
end

-- traverse records the pairs of t and changes or clears some of them, as
-- a function of the key, so that the result doesn't depend on the order
local function traverse(t, out, bynext)
  local salt = rand(1000)
  local seen = {}
  local function visit(k, v)
    local s = repr(k)
    seen[#seen + 1] = s .. "=" .. repr(v)
    local h = salt
    for i = 1, #s do h = h + s:byte(i) * i end
    h = h % 4
    if h == 1 then
      t[k] = nil
    elseif h == 2 then
      t[k] = salt * 10 + #s
    end
  end
  if bynext then
    local k, v = next(t)
    while k ~= nil do
      visit(k, v)
      k, v = next(t, k)
    end
  else
    for k, v in pairs(t) do visit(k, v) end
  end
  table.sort(seen)
  out[#out + 1] = (bynext and "N{" or "P{") .. table.concat(seen, ",") .. "}"
end

local function sequence(n)
  seed = n * 7919 + FUZZ_SEED
  rand(2)
  local size = 4 + rand(36)
  local t = newtable(size)
  local out = {}
  for _ = 1, FUZZ_OPS do
    local op = rand(30)
    if op <= 10 then
      nextval = nextval + 1
      t[key(t, size)] = nextval
    elseif op <= 15 then
      t[key(t, size)] = nil
    elseif op <= 18 then
      out[#out + 1] = "#" .. #t
    elseif op == 19 then
      local k = key(t, size)
      out[#out + 1] = "[" .. repr(k) .. "]" .. repr(t[k])
    elseif op == 20 then
      traverse(t, out, false)
    elseif op == 21 then
      traverse(t, out, true)
    elseif op == 22 then
      nextval = nextval + 1
      table.insert(t, nextval)
      out[#out + 1] = "i" .. #t
    elseif op == 23 then
      nextval = nextval + 1
      local pos = rand(#t + 3) - 1
      table.insert(t, pos, nextval)
      out[#out + 1] = "I" .. pos .. ":" .. #t
    elseif op == 24 then
      out[#out + 1] = "r" .. repr(table.remove(t)) .. ":" .. #t
    elseif op == 25 then
      local pos = rand(#t + 3) - 2
      out[#out + 1] = "R" .. pos .. ":" .. select("#", table.remove(t, pos)) .. ":" .. #t
    elseif op == 26 then
      local c, s = 0, 0
      for i, v in ipairs(t) do c, s = i, s + v end
      out[#out + 1] = "ip" .. c .. ":" .. s
    elseif op == 27 then
      out[#out + 1] = "u" .. select("#", unpack(t))
    elseif op == 28 then
      out[#out + 1] = "m" .. table.maxn(t)
    elseif op == 29 then
      out[#out + 1] = "c" .. tostring((pcall(table.concat, t, ",")))
    else
      out[#out + 1] = "D{" .. dump(t) .. "}"
    end
  end
  out[#out + 1] = "#" .. #t .. " D{" .. dump(t) .. "}"
  print(n .. ": " .. table.concat(out, " "))
end

for n = 1, FUZZ_N do sequence(n) end
