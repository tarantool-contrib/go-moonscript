describe("destructure", function()
  it("should unpack array", function()
    local input = {
      1,
      2,
      3
    }
    local a, b, c
    do
      local _obj_0 = {
        1,
        2,
        3
      }
      a, b, c = _obj_0[1], _obj_0[2], _obj_0[3]
    end
    local d, e, f
    d, e, f = input[1], input[2], input[3]
    assert.same(a, 1)
    assert.same(b, 2)
    assert.same(c, 3)
    assert.same(d, 1)
    assert.same(e, 2)
    return assert.same(f, 3)
  end)
  return it("should destructure", function()
    local futurists = {
      sculptor = "Umberto Boccioni",
      painter = "Vladimir Burliuk",
      poet = {
        name = "F.T. Marinetti",
        address = {
          "Via Roma 42R",
          "Bellagio, Italy 22021"
        }
      }
    }
    local name, street, city
    name, street, city = futurists.poet.name, futurists.poet.address[1], futurists.poet.address[2]
    assert.same(name, "F.T. Marinetti")
    assert.same(street, "Via Roma 42R")
    return assert.same(city, "Bellagio, Italy 22021")
  end)
end)
return describe("function argument destructure", function()
  local with_dev, unindent
  do
    local _obj_0 = require("spec.helpers")
    with_dev, unindent = _obj_0.with_dev, _obj_0.unindent
  end
  local run
  with_dev(function()
    local parse = require("moonscript.parse")
    local compile = require("moonscript.compile")
    run = function(str)
      local tree = assert(parse.string(str))
      local code, err = compile.tree(tree)
      assert(code, err)
      local chunk = assert((loadstring or load)(code))
      return chunk()
    end
  end)
  it("unpacks fields", function()
    local result = run(unindent([[      f = ({:a, :b, :c}) -> a + b + c
      return f {a: 1, b: 2, c: 3}
    ]]))
    return assert.same(6, result)
  end)
  it("unpacks multiple table arguments", function()
    local result = run(unindent([[      dot = ({x: x1, y: y1}, {x: x2, y: y2}) -> x1 * x2 + y1 * y2
      return dot {x: 1, y: 2}, {x: 3, y: 4}
    ]]))
    return assert.same(11, result)
  end)
  it("applies default value", function()
    local result = run(unindent([[      f = ({:a, :b} = {a: 1, b: 2}) -> a + b
      return {f!, f {a: 10, b: 20}}
    ]]))
    return assert.same({
      3,
      30
    }, result)
  end)
  it("shadows outer variable instead of assigning it", function()
    local result = run(unindent([[      x = "outer"
      f = ({:x}) -> x
      inner = f {x: "inner"}
      return {inner, x}
    ]]))
    return assert.same({
      "inner",
      "outer"
    }, result)
  end)
  it("assigns self fields with fat arrow", function()
    local result = run(unindent([[      obj = {total: 10}
      obj.add = ({:count}) => @total + count
      return obj\add {count: 5}
    ]]))
    return assert.same(15, result)
  end)
  it("preserves surrounding args and varargs", function()
    local result = run(unindent([[      f = (first, {:mid}, ...) -> {first, mid, select "#", ...}
      return f "a", {mid: "b"}, "x", "y"
    ]]))
    return assert.same({
      "a",
      "b",
      2
    }, result)
  end)
  it("later defaults see earlier destructured names", function()
    local result = run(unindent([[      a = "outer"
      f = ({:a}, b = a) -> b
      return f {a: 42}
    ]]))
    return assert.same(42, result)
  end)
  it("lowers self assigns after destructure in order", function()
    local result = run(unindent([[      obj = {}
      obj.set = ({:base}, @x = base * 2) => @x
      first = obj\set {base: 3}
      return {first, obj.x, obj\set({base: 3}, 99)}
    ]]))
    return assert.same({
      6,
      6,
      99
    }, result)
  end)
  return it("chains destructure defaults left to right", function()
    local result = run(unindent([[      f = ({:a} = {a: 1}, {:b} = {b: a + 10}) -> {a, b}
      return f!
    ]]))
    return assert.same({
      1,
      11
    }, result)
  end)
end)