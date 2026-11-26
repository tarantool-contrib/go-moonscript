-- go-moonscript: integer keys past the array part of a table (sparse keys,
-- t[0], very large keys), # of sparse tables, table.insert/remove on them.
local function keys(t) local r = {} for k,v in pairs(t) do r[#r+1] = tostring(k)..'='..tostring(v) end table.sort(r) return table.concat(r, ' ') end
local t = {} t[5]=1 t[7]=2 t[0]=0 t[-1]=-1 t[2.5]='f'
print(keys(t), #t)
t[1]=1 print(keys(t), #t)
t[2]=2 t[3]=3 t[4]=4 print(keys(t), #t)
t[6]=6 print(keys(t), #t, t[7], rawget(t, 7))
for k in pairs(t) do t[k]=nil end print(keys(t), #t, next(t))
local n = 0 for i=10,1,-1 do t[i]=i end for k,v in pairs(t) do n = n + 1 end print(n, #t)
print(unpack({[0]='z','a','b'}, 0, 2))
print(unpack({[-1]='y',[0]='z'}, -1, 0))
local s = {} s[1e7] = true s[2^40] = 1 s[4001126229] = 2 print(#s, s[1e7], s[2^40], s[4001126229], keys(s))
local q, first, last = {}, 1, 0
for i = 1, 100000 do last = last + 1 q[last] = i if i % 3 ~= 0 then q[first] = nil first = first + 1 end end
local cnt, sum = 0, 0 for k, v in pairs(q) do cnt = cnt + 1 sum = sum + v end
print(cnt, sum, q[first], q[last], q[first-1])
local a = {1,2,3} table.insert(a, 5, 'x') print(#a, a[5]) table.insert(a, 4, 'y') print(#a, a[4], a[5])
local b = {1,2,3} b[3]=nil print(table.remove(b), #b, b[1], b[2])
local c = {} c[3]=3 table.insert(c, 'a') print(c[1], c[3], c[4], #c)
local d = {} d[2]='x' print(#d, select('#', unpack(d)))
print(#{[20]=0,[600]=0}, #{n=1, [3]=3})
local e = {nil, nil, 3} print(#e) local f = {n=0} for i=1,5 do f[i]=i end print(#f)
local g = {} for i=1,10 do g[i]=i end g[5]=nil print(#g) g[10]=nil g[9]=nil print(#g)
print(select('#', unpack({1, nil, 3})))
local function va(...) return #{...} end print(va(nil, nil, 3), va(1, nil))
t = {} t[10]=nil t[9]=10 t[8]=nil t[7]=10 print(#t)
