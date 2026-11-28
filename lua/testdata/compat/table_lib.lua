-- go-moonscript: table.sort, table.remove and table.maxn on tables whose
-- array part ends with nils, positions out of 1..#t, non-integer keys.
local t = {1,2,3} t[3]=nil
print(table.remove(t), #t, t[1], t[2])
t = {3,1,2} t[3]=nil
print(pcall(table.sort, t)) print(t[1], t[2], t[3])
t = {} t[1]=1 t[2]=2 t[3]=3 t[3]=nil
print(pcall(table.sort, t, function(a,b) return a>b end)) print(t[1], t[2], t[3])
print(table.maxn({[1.5]=1}), table.maxn({1,2,[10]=1}), table.maxn({[-3]=1}), table.maxn({1,2,[2.5]=1}))
print(select('#', table.remove({})), select('#', table.remove({1}, 5)), select('#', table.remove({1}, 0)))
print(pcall(table.remove, {1,2,3}, -1))
t = {1,2,3} print(table.remove(t, 1), #t, t[1], t[2], t[3])
t = {1,2,3} print(table.remove(t, 3), #t, table.remove(t), #t)
-- table.insert is Lua 5.1's tinsert
print(pcall(table.insert, {}, 1, 2, 3))
print(pcall(table.insert, {}))
t = {1, 2, 3} table.insert(t, 1, nil) print(#t, t[1], t[2], t[4])
t = {1, 2, 3} table.insert(t, 0, 'z') print(t[0], t[1], t[2], t[3], t[4])
t = {1, 2, 3} table.insert(t, -1, 'z') print(t[-1], t[0], t[1], t[4])
t = {1, 2, 3} table.insert(t, 6, 'z') print(#t, t[4], t[6])
t = {1, 2, 3} table.insert(t, nil) print(#t, select('#', unpack(t)))
t = {} for i = 1, 10 do table.insert(t, 1, i) end print(table.concat(t, ","))
t = {} for i = 1, 10 do table.insert(t, i) end for i = 1, 5 do table.remove(t, 1) end print(#t, table.concat(t, ","))
t = {n = 1} for i = 1, 40 do table.insert(t, (i % 3) + 1, i) end print(#t, table.concat(t, ","))
print(table.getn({1, 2, nil, 4}), table.getn({1, nil}), table.maxn({}), table.maxn({n = 1, [-1] = 2}))
