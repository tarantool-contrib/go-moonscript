-- Generates third_party/gopher-lua/testdata/compat/lib_pattern_fuzz.lua, a
-- differential test of Lua patterns: random patterns and subjects run
-- through string.find (also plain), string.match, string.gsub and
-- string.gmatch, whose output TestCompat compares with PUC Lua 5.1's (see
-- scripts/gen-pattern-fuzz.sh). Half of the cases are literal patterns
-- (no special characters: pm's bytes.Index fast path), the other half
-- patterns for the matcher. Deterministic: its own PRNG with a fixed seed.
--
--   lua5.1 pattern-fuzz-gen.lua [ncases [seed]] > lib_pattern_fuzz.lua

local ncases = tonumber(arg and arg[1]) or 3000

-- Park-Miller minimal standard generator (exact in doubles)
local seed = tonumber(arg and arg[2]) or 20261126
local function rnd(n) -- 1..n
  seed = (16807 * seed) % 2147483647
  return seed % n + 1
end
local function pick(t) return t[rnd(#t)] end
local function chance(k) return rnd(100) <= k end

local function quote(s)
  return '"' .. s:gsub('[^%w %.,:;!#&\'*+/<=>?@_`{|}~%^%$%(%)%[%]%%%-]', function(c)
    return string.format("\\%03d", c:byte())
  end) .. '"'
end

local function randstr(alphabet, minlen, maxlen)
  local t = {}
  for i = 1, minlen + rnd(maxlen - minlen + 1) - 1 do
    local k = rnd(#alphabet)
    t[i] = alphabet:sub(k, k)
  end
  return table.concat(t)
end

-- literal patterns: no ^$*+?.()[]%-
local litchars = "abcab xy,\255"
local function literalcase()
  local p = randstr(litchars, 1, 3)
  local parts = {}
  for i = 1, rnd(5) - 1 do
    if chance(50) then
      parts[#parts + 1] = p
    elseif chance(30) then
      parts[#parts + 1] = p:sub(1, rnd(#p))
    else
      parts[#parts + 1] = randstr(litchars .. "\0(%.", 0, 4)
    end
  end
  return p, table.concat(parts)
end

-- patterns for the matcher, each with a sample of the text it matches, from
-- which most subjects are made (random patterns seldom match random text);
-- items are {pattern, chars a sample can take}
local single = { { "a", "a" }, { "b", "b" }, { "c", "c" }, { "1", "1" }, { " ", " " },
  { "_", "_" }, { "x", "x" } }
local classes = { { "%a", "aB" }, { "%d", "17" }, { "%s", " \t" }, { "%w", "a1" },
  { "%p", ".(" }, { "%l", "ab" }, { "%u", "B" }, { "%x", "aF1" }, { "%c", "\1\0" },
  { "%A", "1 " }, { "%D", "a " }, { "%S", "a(" }, { "%W", " ." }, { "%P", "a1" },
  { "%L", "B1" }, { "%U", "a" }, { "%X", "g " }, { "%C", "a" }, { "%z", "\0" } }
local escaped = { { "%.", "." }, { "%%", "%" }, { "%(", "(" }, { "%)", ")" }, { "%[", "[" },
  { "%]", "]" }, { "%-", "-" }, { "%*", "*" }, { "%+", "+" }, { "%?", "?" }, { "%^", "^" },
  { "%$", "$" } }
local sets = { { "[abc]", "abc" }, { "[^ab]", "c1" }, { "[a-c]", "bc" }, { "[%d_]", "1_" },
  { "[^%s]", "a." }, { "[%a%d]", "a1" }, { "[ab-]", "b-" }, { "[-a]", "-a" },
  { "[^%a]", "1 " }, { "[%%]", "%" }, { "[a%-c]", "a-" }, { "[%]]", "]" }, { "[]]", "]" },
  { "[^]a]", "b" }, { "[%z]", "\0" }, { "[1-3x]", "2x" }, { "[%w_.]", "a_." },
  { "[%a-z]", "a-z" }, { "[a-c-e]", "b-e" }, { "[a-%d]", "d" }, { "[%w-_]", "a-_" },
  { "[]-a]", "]^a" }, { "[^-]", "a" } }
-- frontiers, and malformed items: Lua 5.1 raises their errors only when the
-- matcher reaches them
local frontiers = { "%f[%a]", "%f[%s]", "%f[^%w]", "%f[a-c]", "%f[%z]", "%f[^%z]" }
local malformed = { "%", "%b", "%bx", "[a", "[^", "[%", ")", "%f", "%fa", "%0", "x%" }
local quantifiers = { "*", "+", "-", "?" }
local noise = "abc1 _x.()%-ab\0"

local function sample(chars) local k = rnd(#chars) return chars:sub(k, k) end

local function atom()
  local k = rnd(10)
  if k <= 4 then return pick(single)
  elseif k == 5 then return { ".", noise }
  elseif k <= 7 then return pick(classes)
  elseif k == 8 then return pick(escaped)
  else return pick(sets) end
end

local function sequence(depth, st)
  local t, smp = {}, {}
  for i = 1, rnd(4) do
    local k = rnd(20)
    if k <= 2 and depth < 2 and st.ncap < 30 then
      st.ncap = st.ncap + 1
      local n = st.ncap
      local p, sm = sequence(depth + 1, st)
      t[#t + 1], smp[#smp + 1] = "(" .. p .. ")", sm
      st.closed[#st.closed + 1], st.text[n] = n, sm
    elseif k == 3 and st.ncap < 30 then
      st.ncap = st.ncap + 1
      t[#t + 1] = "()"
      if chance(30) then st.closed[#st.closed + 1], st.text[st.ncap] = st.ncap, "" end
    elseif k == 4 then
      local b = pick({ { "%b()", "(", ")" }, { "%bab", "a", "b" }, { "%b((", "(", "(" }, { "%bxy", "x", "y" } })
      t[#t + 1], smp[#smp + 1] = b[1], b[2] .. randstr(noise, 0, 3) .. b[3]
    elseif k == 6 then
      t[#t + 1] = pick(frontiers)
    elseif k == 5 and #st.closed > 0 then
      local n = pick(st.closed)
      if n <= 9 then t[#t + 1], smp[#smp + 1] = "%" .. n, st.text[n] end
    else
      local a = atom()
      local q = chance(45) and pick(quantifiers) or ""
      local reps = ({ [""] = 1, ["*"] = rnd(4) - 1, ["+"] = rnd(3), ["-"] = rnd(4) - 1, ["?"] = rnd(2) - 1 })[q]
      t[#t + 1] = a[1] .. q
      for j = 1, reps do smp[#smp + 1] = sample(a[2]) end
    end
  end
  return table.concat(t), table.concat(smp)
end

local function matchercase()
  local st = { ncap = 0, closed = {}, text = {} }
  local p, smp = sequence(0, st)
  if chance(20) then p = "^" .. p end
  if chance(5) then p = p .. pick(malformed) end
  if chance(20) then p = p .. "$" end
  if chance(1) then p = "" end
  local s
  if chance(25) then
    s = randstr(noise, 0, 16)
  else
    s = randstr(noise, 0, 4) .. smp .. randstr(noise, 0, 4)
    if chance(30) then s = s .. smp .. randstr(noise, 0, 3) end
    if chance(15) and #s > 0 then -- one byte changed
      local k = rnd(#s)
      s = s:sub(1, k - 1) .. sample(noise) .. s:sub(k + 1)
    end
  end
  return p, s
end

local inits = { "nil", "1", "2", "3", "-1", "-2", "-30", "0", "100" }
local ns = { "nil", "nil", "nil", "1", "2" }
local nrepl = 9 -- R[1..nrepl] in the driver

io.write([[
-- Generated by scripts/gen-pattern-fuzz.sh (scripts/pattern-fuzz-gen.lua):
-- do not edit. Random patterns and subjects run through string.find, plain
-- find, match, gsub and gmatch; the reference output is PUC Lua 5.1's.
-- Errors print as "E" (gopher-lua's messages differ). Cases 1..]] .. ncases / 2 .. [[ are
-- literal patterns (pm's fast path), the rest are for the matcher.
local function enc(...)
  local t = {}
  for i = 1, select("#", ...) do
    local v = select(i, ...)
    if type(v) == "string" then
      t[i] = '"' .. v:gsub("[^%w%p ]", function(c) return "\\" .. c:byte() end) .. '"'
    else
      t[i] = tostring(v)
    end
  end
  return table.concat(t, ",")
end
local function result(ok, ...)
  if not ok then return "E" end
  return enc(...)
end
local function try(f, ...) return result(pcall(f, ...)) end
local R = {
  "<%0>", "%1", "-", "%%", "%a", "", "[%1|%0]",
  { a = "A", ["1"] = 1, ab = false },
  function(...) if select("#", ...) > 1 then return select(2, ...) end return (...) .. "!" end,
}
local function gm(s, p)
  local t = {}
  for a, b in string.gmatch(s, p) do
    t[#t + 1] = enc(a, b)
    if #t > 50 then break end
  end
  return table.concat(t, ";")
end
local cases = {
]])
for i = 1, ncases do
  local p, s
  if i <= ncases / 2 then p, s = literalcase() else p, s = matchercase() end
  io.write(string.format("{%s,%s,%s,%d,%s},\n", quote(p), quote(s), pick(inits), rnd(nrepl), pick(ns)))
end
io.write([==[
}
for i, c in ipairs(cases) do
  local p, s, init, r, n = c[1], c[2], c[3], R[c[4]], c[5]
  print(i, try(string.find, s, p, init), try(string.find, s, p, init, true),
    try(string.match, s, p, init), try(string.gsub, s, p, r, n), try(gm, s, p))
end
]==])
