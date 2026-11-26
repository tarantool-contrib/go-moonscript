-- A compile-like workload: tokenize, parse (recursive descent with
-- precedence climbing), constant-fold and compile a small assignment
-- language to Lua, with the helpers of our compiled moonscript.util
-- (split, trim, pos_to_line, get_line, dump); the generated Lua is loaded
-- and run.
import split, trim, pos_to_line, get_line, dump from require "moonscript.util"

N = tonumber(arg and arg[1]) or 10

prec = {"+": 1, "-": 1, "*": 2, "/": 2}

gen_source = (seed) ->
  lines = {}
  for i = 1, 40
    a = (seed * 31 + i * 17) % 97
    b = (seed + i * 7) % 13 + 1
    prev = i > 1 and "v#{i - 1}" or "1"
    lines[#lines + 1] = "  v#{i} = (#{prev} + #{a}) * #{b} / (#{b} + 1) - #{prev} / (#{b} + 2) + (2 * 3 - #{a}) / 4  "
  table.concat lines, "\n"

class Lexer
  new: (@src) =>
    @pos = 1
    @tokens = {}

  push: (kind, text) =>
    @tokens[#@tokens + 1] = {:kind, :text, pos: @pos}
    @pos += #text

  run: =>
    src = @src
    while true
      s, e = src\find "^%s+", @pos
      @pos = e + 1 if s
      break if @pos > #src
      num = src\match "^%d+%.?%d*", @pos
      if num
        @push "num", num
        continue
      name = src\match "^[%a_][%w_]*", @pos
      if name
        @push "name", name
        continue
      op = src\sub @pos, @pos
      if op\find "^[%+%-%*/%(%)=]"
        @push "op", op
        continue
      error "unexpected #{op} at line #{pos_to_line src, @pos}"
    @tokens

class Parser
  new: (@tokens) => @i = 1

  peek: => @tokens[@i]

  next: =>
    t = @tokens[@i]
    @i += 1
    t

  expect: (text) =>
    t = @next!
    error "expected #{text}" unless t and t.text == text
    t

  primary: =>
    t = @next!
    switch t.kind
      when "num"
        {"num", tonumber t.text}
      when "name"
        {"var", t.text}
      else
        if t.text == "("
          e = @expr 0
          @expect ")"
          e
        elseif t.text == "-"
          {"neg", @primary!}
        else
          error "unexpected #{t.text}"

  expr: (min) =>
    lhs = @primary!
    while true
      t = @peek!
      break unless t and t.kind == "op"
      p = prec[t.text]
      break unless p and p > min
      @next!
      lhs = {"bin", t.text, lhs, @expr p}
    lhs

  statement: =>
    name = @next!
    @expect "="
    {"assign", name.text, @expr 0}

fold = (node) ->
  switch node[1]
    when "bin"
      a, b = fold(node[3]), fold(node[4])
      if a[1] == "num" and b[1] == "num"
        x, y = a[2], b[2]
        v = switch node[2]
          when "+" then x + y
          when "-" then x - y
          when "*" then x * y
          when "/" then x / y
        {"num", v}
      else
        {"bin", node[2], a, b}
    when "neg"
      a = fold node[2]
      a[1] == "num" and {"num", -a[2]} or {"neg", a}
    when "assign"
      {"assign", node[2], fold node[3]}
    else
      node

emit = (node) ->
  switch node[1]
    when "num" then string.format "%.17g", node[2]
    when "var" then "env.#{node[2]}"
    when "neg" then "(-#{emit node[2]})"
    when "bin" then "(#{emit node[3]} #{node[2]} #{emit node[4]})"

compile = (src) ->
  out = {"local env = ..."}
  dumped = 0
  for line in *split src, "\n"
    line = trim line
    continue if line == ""
    ast = fold Parser(Lexer(line)\run!)\statement!
    dumped += #dump ast if #out % 10 == 0
    out[#out + 1] = "env.#{ast[2]} = #{emit ast[3]}"
  out[#out + 1] = "return env"
  table.concat(out, "\n"), dumped

sum, size = 0, 0
for i = 1, N
  code, dumped = compile gen_source i
  env = assert(loadstring code)({})
  sum += env.v40
  size += #code + dumped + pos_to_line(code, #code) + #get_line(code, 3)

print string.format("%.6f", sum), size
