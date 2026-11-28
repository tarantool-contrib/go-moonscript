local unindent, with_dev
do
  local _obj_0 = require("spec.helpers")
  unindent, with_dev = _obj_0.unindent, _obj_0.with_dev
end
local split
split = require("moonscript.util").split
local lua_load = loadstring or load
return describe("correlate compile", function()
  local to_lua
  with_dev(function()
    to_lua = require("moonscript.base").to_lua
  end)
  local compile
  compile = function(code)
    local lua_code, posmap = assert(to_lua(code, {
      correlate = true
    }))
    assert(lua_load(lua_code), "correlate output is not valid lua")
    return lua_code, posmap
  end
  it("places functions on their source lines", function()
    local code = unindent([[      first = ->
        "hello"

      second = (a, b) ->
        a + b

      {first, second}
    ]])
    local lua_code = compile(code)
    local fns = assert(lua_load(lua_code))()
    assert.same(1, debug.getinfo(fns[1], "S").linedefined)
    return assert.same(4, debug.getinfo(fns[2], "S").linedefined)
  end)
  it("reports runtime error at source line", function()
    local code = unindent([[      fn = ->
        error "boom"
        nil

      fn!
    ]])
    local lua_code = compile(code)
    local chunk = assert(lua_load(lua_code, "@correlate_test"))
    local ok, err = pcall(chunk)
    assert.same(false, ok)
    return assert.matches("correlate_test:2: boom", err)
  end)
  it("pads lines to match source", function()
    local code = unindent([[      -- comment
      -- comment
      x = 100
      x
    ]])
    local lua_code = compile(code)
    local lines = split(lua_code, "\n")
    assert.same("", lines[1])
    assert.same("", lines[2])
    assert.matches("x = 100", lines[3])
    return assert.matches("return x", lines[4])
  end)
  it("separates ambiguous statements when padding", function()
    local code = unindent([[      x = tostring
      (x or error) "hi"
      1234
    ]])
    local lua_code = compile(code)
    return assert.same(1234, assert(lua_load(lua_code))())
  end)
  it("compiles class to valid lua on its source line", function()
    local code = unindent([[      class Thing
        new: (@x) =>
        double: => @x * 2

      t = Thing 5
      t\double!
    ]])
    local lua_code = compile(code)
    assert.same(10, assert(lua_load(lua_code))())
    return assert.matches("local Thing do", split(lua_code, "\n")[1])
  end)
  it("keeps multiline strings intact", function()
    local code = unindent([[      x = [==[
      one
      two
      ]==]
      y = "last"
      {x, y}
    ]])
    local lua_code = compile(code)
    local result = assert(lua_load(lua_code))()
    assert.same("one\ntwo\n", result[1])
    return assert.matches([[y = "last"]], split(lua_code, "\n")[5])
  end)
  it("reports error line inside if expression assign", function()
    local code = unindent([[      x = if true
        error "boom"
    ]])
    local lua_code = compile(code)
    local chunk = assert(lua_load(lua_code, "@correlate_test"))
    local ok, err = pcall(chunk)
    assert.same(false, ok)
    return assert.matches("correlate_test:2: boom", err)
  end)
  it("reports error line inside switch expression assign", function()
    local code = unindent([[      v = 2
      x = switch v
        when 1
          "one"
        else
          error "boom"
    ]])
    local lua_code = compile(code)
    local chunk = assert(lua_load(lua_code, "@correlate_test"))
    local ok, err = pcall(chunk)
    assert.same(false, ok)
    return assert.matches("correlate_test:6: boom", err)
  end)
  it("correlates against the right source when options are reused", function()
    local options = {
      correlate = true
    }
    assert(to_lua("a = 1", options))
    local lua_code = assert(to_lua("-- comment\n-- comment\nb = 2", options))
    assert.same(nil, options.source)
    return assert.matches("b = 2", split(lua_code, "\n")[3])
  end)
  return it("resolves marked positions to their own line or earlier", function()
    local pos_to_line
    pos_to_line = require("moonscript.util").pos_to_line
    local code = unindent([[      add = (a, b) ->
        a + b

      class Counter
        new: => @count = 0
        increment: =>
          @count += 1
          @count

      c = Counter!
      for i=1,10
        c\increment!

      print add c.count, 5
    ]])
    local _, posmap = compile(code)
    for lua_line, pos in pairs(posmap) do
      assert(pos_to_line(code, pos) <= lua_line, "posmap entry for line " .. tostring(lua_line) .. " resolves past it")
    end
  end)
end)