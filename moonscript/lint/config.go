package lint

// whitelist_for_file: per-file extra globals from the lint_config module.
//
//	-- lint_config.lua, found on package.path (./lint_config.lua first)
//	return {
//	  whitelist_globals = {
//	    ["some_file_pattern"] = { "some_var", "another_var" },
//	  },
//	}
//
// The module is plain Lua run by gopher-lua (a Lua 5.1 VM); file patterns
// are Lua patterns matched against the file name as given to moonc.

import (
	"errors"
	"os"
	"strings"
	"sync"

	lua "github.com/tarantool-contrib/go-moonscript/lua"
)

// luaDefaultPath is package.path of the reference moonc: its launcher
// prepends the luarocks tree to lua5.1's default path (or LUA_PATH).
func luaDefaultPath() string {
	const def = "./?.lua;/usr/local/share/lua/5.1/?.lua;/usr/local/share/lua/5.1/?/init.lua;" +
		"/usr/local/lib/lua/5.1/?.lua;/usr/local/lib/lua/5.1/?/init.lua;" +
		"/usr/share/lua/5.1/?.lua;/usr/share/lua/5.1/?/init.lua"
	path := def
	if env, ok := os.LookupEnv("LUA_PATH"); ok {
		path = strings.Replace(env, ";;", ";"+def+";", 1)
	}
	return "/usr/local/share/lua/5.1/?.lua;/usr/local/share/lua/5.1/?/init.lua;" + path
}

const whitelistLua = `
local lint_config
return function(fname)
  if not lint_config then
    lint_config = {}
    pcall(function() lint_config = require "lint_config" end)
  end
  if not lint_config.whitelist_globals then
    return nil
  end
  local final_list = {}
  for pattern, list in pairs(lint_config.whitelist_globals) do
    if fname:match(pattern) then
      for i = 1, #list do
        final_list[#final_list + 1] = list[i]
      end
    end
  end
  return final_list
end
`

var config struct {
	sync.Mutex
	L  *lua.LState
	fn lua.LValue
}

// WhitelistForFile returns the whitelist for a file: the default one, plus
// the lint_config whitelist_globals entries whose pattern matches fname.
// The lint_config module is loaded once, on the first call.
func WhitelistForFile(fname string) (Whitelist, error) {
	config.Lock()
	defer config.Unlock()
	L := config.L
	if L == nil {
		L = lua.NewState()
		L.SetField(L.GetField(L.Get(lua.GlobalsIndex), "package"), "path", lua.LString(luaDefaultPath()))
		if err := L.DoString(whitelistLua); err != nil {
			return nil, &Crash{Msg: err.Error()}
		}
		config.L, config.fn = L, L.Get(-1)
		L.Pop(1)
	}
	if err := L.CallByParam(lua.P{Fn: config.fn, NRet: 1, Protect: true}, lua.LString(fname)); err != nil {
		msg := err.Error()
		var apiErr *lua.ApiError
		if errors.As(err, &apiErr) {
			msg = apiErr.Object.String()
		}
		return nil, &Crash{Msg: msg}
	}
	ret := L.Get(-1)
	L.Pop(1)
	list, ok := ret.(*lua.LTable)
	if !ok {
		return nil, nil
	}
	extra := map[string]bool{}
	list.ForEach(func(_, v lua.LValue) {
		if s, ok := v.(lua.LString); ok {
			extra[string(s)] = true
		}
	})
	return func(name string) bool { return extra[name] || DefaultWhitelist[name] }, nil
}

// ResetConfig forgets the loaded lint_config module, as if a new moonc
// process started (tests run several moonc invocations in one process).
func ResetConfig() {
	config.Lock()
	defer config.Unlock()
	if config.L != nil {
		config.L.Close()
	}
	config.L, config.fn = nil, nil
}
