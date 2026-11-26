-- PUC Lua 5.1: no coroutine.isyieldable, package.searchpath,
-- debug.upvalueid or debug.upvaluejoin; debug.getinfo has no nparams;
-- debug.getlocal takes a level; os.date copies "%E" and "%O" (each
-- conversion goes to strftime alone)
print(coroutine.isyieldable, package.searchpath, debug.upvalueid, debug.upvaluejoin)
print(debug.getinfo(function(x, y, ...) end, "u").nparams, (pcall(debug.getlocal, function(x) end, 1)))
print(os.date("!%Ey|%Oy|%Ez|%E", 0))
