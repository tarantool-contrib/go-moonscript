-- go-moonscript: nil and NaN table keys (yuin/gopher-lua#319), rawset.
local t = {}
print(rawset(t, 1, 2) == t, select('#', rawset(t, 2, 3)), t[1], t[2])
print(pcall(rawset, t, nil, 1))
print(pcall(rawset, t, 0/0, 1))
print(pcall(rawset, t, nil, nil))
print(pcall(function() rawset(t, 0/0, 1) end))
print(pcall(function() local u = {} u[nil] = 1 end))
print(pcall(function() local u = {} u[0/0] = 1 end))
print(pcall(function() return {[0/0] = 1} end))
print(rawget(t, nil), rawget(t, 0/0), t[nil], t[0/0])
local n = 0 for k, v in pairs(t) do n = n + 1 end print(n)
