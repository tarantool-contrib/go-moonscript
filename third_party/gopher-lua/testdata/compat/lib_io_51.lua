-- the io library as Lua 5.1's liolib.c over glibc stdio (print only: the
-- test replaces it, io.write goes to the real standard output)
local function try(f, ...)
  local args, n = {...}, select("#", ...)
  local r = {pcall(function() return f(unpack(args, 1, n)) end)}
  if type(r[2]) == "string" then
    -- (the quotes of function names in argument errors are the errors area's)
    r[2] = r[2]:gsub("to '(%w+)'", "to %1"):gsub("FILE%* expected, got %w+", "FILE* expected")
      :gsub("%(invalid option.*", "(invalid option)"):gsub("/tmp/lua_%w+", "<tmp>")
  end
  return unpack(r, 1, n + 3)
end
local function addr(s) return (tostring(s):gsub("0x%x+", "0x?")) end
local name = os.tmpname()

print(addr(io.stdin), addr(io.stdout), addr(io.stderr))
print(io.type(io.stdout), io.type(42), io.type({}), io.type(nil), try(io.type))
print(io.stdout:close())
print(io.stderr:close())
print(io.close(io.stdin))
print(io.type(io.stdout), io.stdout:write(""), io.write())

-- write, read
local f = assert(io.open(name, "w"))
print(addr(f), io.type(f))
print(f:write("line 1\n", 2, " ", 3.25, "\n", "\n", "last"))
print(f:seek(), f:seek("set"), f:seek("end"), f:seek("cur", -2), f:seek("set", 3))
print(f:setvbuf("no"), f:setvbuf("full", 1024), f:setvbuf("line"))
print(try(f.setvbuf, f, "bad"))
print(f:read("*l"))
print(f:close())
print(tostring(f), io.type(f))
print(try(f.write, f, "x"))
print(try(f.read, f))
print(try(f.close, f))
print(try(f.lines, f))
print(try(f.seek, f))
print(try(io.close, f))
f = assert(io.open(name))
print(f:read(), f:read("*l"), f:read("*l"), f:read("*l"), f:read("*l"), f:read("*l"))
print(f:seek("set"), f:read(0), f:read(3), f:read("*a"), f:read(0), f:read(1), f:read("*a"), f:read("*l"))
f:seek("set")
print(f:read("*n", "*n", "*n", "*l"))
print(f:read("*n"), f:read("*l"))
print(try(f.read, f, "x"), try(f.read, f, "*x"), try(f.read, f, {}))
print(f:write("x"))
f:close()

-- numbers as fscanf "%lf" reads them
f = assert(io.open(name, "w"))
f:write("  12  -3.5e2 0x1F .5 1e 7 +inf -nan 0x 12abc - 99\n1E+2x")
f:close()
f = assert(io.open(name))
local nums = {}
for i = 1, 6 do nums[i] = tostring(f:read("*n")) end
print(table.concat(nums, " "), f:read(2))
print(f:read("*n"), f:read(1), f:read("*n"), f:read(3), f:read("*n"), f:read("*n"), f:read("*a"))
f:close()

-- lines: nothing at the end; the file of io.lines(name) is closed then
f = assert(io.open(name, "w"))
f:write("a\n\nb")
f:close()
local ls = {}
for l in io.lines(name) do ls[#ls + 1] = "[" .. l .. "]" end
print(table.concat(ls))
f = assert(io.open(name))
local it = f:lines()
print(it(), it(), it(), select("#", it()), io.type(f))
f:close()
print(try(it))
it = io.lines(name)
print(it(), it(), it(), select("#", it()))
print(try(it))
print(try(io.lines, name .. ".none"))

-- io.input, io.output: nil gives the current one
print(io.input() == io.stdin, io.input(nil) == io.stdin, io.output(nil) == io.stdout)
print(addr(io.input(name)), io.input() ~= io.stdin)
print(io.read(), io.read("*a"), io.read())
for l in io.lines() do print("never", l) end
io.input():close()
print(try(io.read))
print(try(io.lines))
print(try(io.input, name .. ".none"))
print(try(io.input, {}))
print(io.input(io.stdin) == io.stdin)
local out = io.output(name)
print(io.output() == out, io.write("one ", 1, "\n"), io.output():write("two\n"))
print(io.close())
print(try(io.write, "x"))
print(try(io.flush))
io.output(io.stdout)
print(io.open(name):read("*a"))

-- update mode: reading after writing and writing after reading
f = assert(io.open(name, "w+"))
f:write("hello world")
f:seek("set", 6)
print(f:read(3))
f:write("LD")
f:seek("set")
print(f:read("*a"))
f:close()
f = assert(io.open(name, "r+"))
print(f:read(5))
f:write("!")
f:seek("set")
print(f:read("*a"))
f:close()

-- failures: nil, strerror, errno
print((select(2, io.open(name .. ".none"))):gsub("^.*: ", ""), select(3, io.open(name .. ".none")))
print(addr(io.open(name, "rw+")))
f = assert(io.open(name, "w"))
print(f:read())
f:close()
f = assert(io.open(name))
print(f:write("x"))
f:close()
f = assert(io.open("/"))
print(f:read(), f:read("*a"))
f:close()
print(io.open("/", "w"))

-- processes
local p = assert(io.popen("echo out; exit 3"))
print(addr(p), io.type(p), p:read("*a"))
print(p:close())
print(io.type(p), tostring(p))
p = assert(io.popen("cat > " .. name, "w"))
print(p:write("to the process ", 1))
print(p:close())
print(io.open(name):read("*a"))
print(select(2, io.popen("true", "x")), select(3, io.popen("true", "rw")))
p = assert(io.popen("exit 0"))
print(p:seek())
p:close()

-- tmpfile
f = io.tmpfile()
print(io.type(f), f:write("tmp"), f:seek("set"), f:read("*a"))
f:close()
os.remove(name)
