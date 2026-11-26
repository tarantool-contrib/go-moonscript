-- go-moonscript: % is Lua 5.1's a - floor(a/b)*b, math.mod is math.fmod.
local function f(x) return string.format("%.17g", x) end
local a, b, c = 3, 0.1, 1e308
print(f(a % b), f(c % 3), f(7 % 0.3), f(5.3 % 2), f(-5.3 % 2), f(5.3 % -2), f(-5 % 3), f(5 % -3))
local nz, inf = -0.0, 1/0
print(1/(nz % 5) > 0, 5 % inf ~= 5 % inf, -5 % inf ~= -5 % inf, 5 % -inf ~= 5 % -inf, inf % 2 ~= inf % 2, 5 % 0 ~= 5 % 0)
print(3 % 0.1 == a % b, 1e308 % 3 == c % 3, 5.5 % 2, -5.5 % 2, 2^53 % 3)
print(math.mod(-5, 3), math.fmod(-5, 3), math.mod(5.5, -2), math.fmod(5.5, -2))
