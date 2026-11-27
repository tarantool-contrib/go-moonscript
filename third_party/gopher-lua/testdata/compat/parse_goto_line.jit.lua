-- The line of a goto or break in LuaJIT's errors ("undefined label", "no
-- loop to break", "jumps into the scope of local") is the line of its jump
-- instruction. Right after a block whose locals are upvalues, which ends
-- with a UCLO, bcemit_jmp merges the jump into that UCLO: the goto gets the
-- line of the block's last token. Any statement but an empty do block
-- emits code or a jump target in between; loops, if and repeat end with
-- their own jumps. luajit is the reference.
local function try(name, src)
  local f, err = loadstring(src, "=src")
  print(name, f and "ok" or (err:gsub("^[^:]*:", "")))
end
local U = "do local x f = function() return x end end"
try("after do", U .. "\n\ngoto nowhere")
try("last token", "do\n  local x\n  f = function() return x end\n\nend\n\ngoto nowhere")
try("inner do", "do\n do\n  local x\n  f = function() return x end\n end\nend\n\ngoto nowhere")
try("both do", "do\n local y\n g = function() return y end\n do\n  local x\n  f = function() return x end\n end\nend\n\ngoto nowhere")
try("semicolon", U .. "\n;\ngoto nowhere")
try("empty do", U .. "\ndo end\ngoto nowhere")
try("while", "while c do local x f = function() return x end end\n\ngoto nowhere")
try("into scope", U .. "\ngoto l\nlocal y\n::l::\nprint(y)")
try("break", "function g()\n" .. U .. "\nbreak\nend")
try("two gotos", U .. "\ngoto a\ngoto b")
try("repeat", "repeat local x f = function() return x end until x\n\ngoto nowhere")
try("if", "if a then " .. U .. " end\n\ngoto nowhere")
try("else", "if c then\n" .. U .. "\nelse\ngoto nowhere\nend")
try("elseif", "if c then\n" .. U .. "\nelseif d then\nbreak\nend")
try("for", "for i = 1, 2 do local x f = function() return x end end\n\ngoto nowhere")
try("for var", "for i = 1, 2 do f = function() return i end end\n\ngoto nowhere")
try("return", "do local x f = function() return x end return end\n\ngoto nowhere")
try("label", U .. "\n::l::\ngoto nowhere")
try("inside", "do local x f = function() return x end\ngoto nowhere end")
try("main local", "local x f = function() return x end\ndo end\ngoto nowhere")
try("if in do", "do local x f = function() return x end if a then b() end end\n\ngoto nowhere")
try("label in do", "do local x f = function() return x end ::m:: end\n\ngoto nowhere")
try("deep closure", "do local x f = function() return function() return x end end end\n\ngoto nowhere")
try("local", U .. "\nlocal z\ngoto nowhere")
try("name line", U .. "\n\ngoto\nnowhere")
try("goto in if", U .. "\n\nif a then goto nowhere end")
try("goto in do", U .. "\n\ndo goto nowhere end")
try("break in do", U .. "\n\ndo break end")
try("after function", "local function q() end " .. U .. "\n\ngoto nowhere")
try("first goto", U .. "\n\ngoto l\ngoto nowhere\nlocal y ::l:: y()")
try("in function", "local function q()\n" .. U .. "\n\ngoto nowhere\nend")
try("in loop", "while a do\n" .. U .. "\n\nbreak\nend\ngoto nowhere")
try("resolved", U .. "\n\ngoto l\n::l::")
try("backward", "::l::\n" .. U .. "\n\ngoto l\ngoto nowhere")
