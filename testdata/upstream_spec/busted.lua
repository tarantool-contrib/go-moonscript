-- A minimal stand-in for busted + luassert, enough to run the upstream
-- MoonScript v0.8.0 specs in gopher-lua (see spec_runtime_test.go).
--
-- Loading a spec chunk registers its describe/it blocks; the Go side then
-- calls busted.run(i) for each registered test so every `it` becomes a Go
-- subtest. Supported: describe, it, setup/lazy_setup, teardown, before_each,
-- after_each, pending, assert(...) and the luassert assertions the specs use,
-- spy.on / assert.spy(s).was.called.

local busted = { tests = {} }

local root = { name = nil, parent = nil, setups = {}, teardowns = {},
  before = {}, after = {}, did_setup = false }
local current = root

local function full_name(block, name)
  local parts = { name }
  while block and block.name do
    table.insert(parts, 1, block.name)
    block = block.parent
  end
  return table.concat(parts, " ")
end

function describe(name, fn)
  local block = { name = name, parent = current, setups = {}, teardowns = {},
    before = {}, after = {}, did_setup = false }
  local prev = current
  current = block
  fn()
  current = prev
end

function it(name, fn)
  table.insert(busted.tests, { name = full_name(current, name), fn = fn, block = current })
end

function pending(name)
  table.insert(busted.tests, { name = full_name(current, name), pending = true, block = current })
end

function setup(fn) table.insert(current.setups, fn) end
lazy_setup = setup
function teardown(fn) table.insert(current.teardowns, fn) end
lazy_teardown = teardown
function before_each(fn) table.insert(current.before, fn) end
function after_each(fn) table.insert(current.after, fn) end

package.loaded.busted = {
  describe = describe, it = it, pending = pending, setup = setup,
  teardown = teardown, before_each = before_each, after_each = after_each,
}

local function chain(block)
  local out = {}
  while block do
    table.insert(out, 1, block)
    block = block.parent
  end
  return out
end

-- busted.run(i) -> name, ok, err ("pending" for pending tests)
function busted.run(i)
  local t = busted.tests[i]
  if t.pending then return t.name, "pending" end
  local blocks = chain(t.block)
  local ok, err = pcall(function()
    for _, b in ipairs(blocks) do
      if not b.did_setup then
        b.did_setup = true
        for _, f in ipairs(b.setups) do f() end
      end
    end
    for _, b in ipairs(blocks) do
      for _, f in ipairs(b.before) do f() end
    end
    t.fn()
  end)
  for i = #blocks, 1, -1 do
    for _, f in ipairs(blocks[i].after) do
      local aok, aerr = pcall(f)
      if ok and not aok then ok, err = aok, aerr end
    end
  end
  return t.name, ok, err
end

function busted.count() return #busted.tests end

-- luassert --------------------------------------------------------------------

local function deepcompare(t1, t2)
  if type(t1) ~= "table" or type(t2) ~= "table" then return t1 == t2 end
  local mt1, mt2 = getmetatable(t1), getmetatable(t2)
  if mt1 and mt1 == mt2 and mt1.__eq then return t1 == t2 end
  if rawequal(t1, t2) then return true end
  for k, v1 in next, t1 do
    local v2 = rawget(t2, k)
    if v2 == nil or not deepcompare(v1, v2) then return false end
  end
  for k in next, t2 do
    if rawget(t1, k) == nil then return false end
  end
  return true
end
busted.deepcompare = deepcompare

local function dump(v, depth)
  depth = depth or 0
  if type(v) == "string" then return string.format("%q", v) end
  if type(v) ~= "table" then return tostring(v) end
  if depth > 4 then return "{...}" end
  local keys = {}
  for k in next, v do table.insert(keys, k) end
  table.sort(keys, function(a, b)
    if type(a) == type(b) and (type(a) == "number" or type(a) == "string") then return a < b end
    return type(a) < type(b)
  end)
  local parts = {}
  for _, k in ipairs(keys) do
    table.insert(parts, "[" .. dump(k, depth + 1) .. "] = " .. dump(v[k], depth + 1))
  end
  return "{" .. table.concat(parts, ", ") .. "}"
end
busted.dump = dump

local function fail(msg, level)
  error(msg, (level or 1) + 2)
end

local checks = {}

function checks.same(expected, actual)
  if not deepcompare(expected, actual) then
    fail("expected same\n  expected: " .. dump(expected) .. "\n  actual:   " .. dump(actual))
  end
end

function checks.are_same(...) return checks.same(...) end

function checks.equal(expected, actual)
  if expected ~= actual then
    fail("expected equal\n  expected: " .. dump(expected) .. "\n  actual:   " .. dump(actual))
  end
end
checks.equals = checks.equal

function checks.is_true(v, msg)
  if v ~= true then fail(msg or ("expected true, got " .. dump(v))) end
end

function checks.is_false(v, msg)
  if v ~= false then fail(msg or ("expected false, got " .. dump(v))) end
end

function checks.is_nil(v, msg)
  if v ~= nil then fail(msg or ("expected nil, got " .. dump(v))) end
end

function checks.truthy(v, msg)
  if not v then fail(msg or ("expected truthy, got " .. dump(v))) end
end
checks.is_truthy = checks.truthy

function checks.falsy(v, msg)
  if v then fail(msg or ("expected falsy, got " .. dump(v))) end
end
checks.is_falsy = checks.falsy

function checks.is_string(v, msg)
  if type(v) ~= "string" then fail(msg or ("expected a string, got " .. dump(v))) end
end

function checks.is_table(v, msg)
  if type(v) ~= "table" then fail(msg or ("expected a table, got " .. dump(v))) end
end

function checks.matches(pattern, str, msg)
  if type(str) ~= "string" or not str:find(pattern) then
    fail(msg or ("expected " .. dump(str) .. " to match " .. dump(pattern)))
  end
end

-- luassert's has_error: the "chunk:line: " prefix of a string error is
-- removed before comparing
function checks.has_error(fn, expected)
  local ok, err = pcall(fn)
  if ok then fail("expected an error") end
  if expected ~= nil then
    if type(err) == "string" then err = err:gsub("^.-:%d+: ", "", 1) end
    if not deepcompare(err, expected) then
      fail("expected error " .. dump(expected) .. ", got " .. dump(err))
    end
  end
end
checks.has_errors = checks.has_error
checks.error = checks.has_error

function checks.spy(s)
  return { was = {
    called = function(n)
      if n == nil then
        if #s.calls == 0 then fail("expected spy to be called") end
      elseif #s.calls ~= n then
        fail("expected spy to be called " .. n .. " times, got " .. #s.calls)
      end
    end,
  } }
end

assert = setmetatable(checks, {
  __call = function(_, v, msg, ...)
    if not v then error(msg or "assertion failed!", 2) end
    return v, msg, ...
  end,
})

spy = {}
function spy.new(fn)
  local s = { calls = {} }
  return setmetatable(s, { __call = function(self, ...)
    table.insert(self.calls, { ... })
    if fn then return fn(...) end
  end })
end
function spy.on(tbl, name)
  local s = spy.new(tbl[name])
  tbl[name] = s
  return s
end

return busted
