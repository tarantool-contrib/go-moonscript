util = require "lib.util"
print "name", util.name
print "add", util.add 2, 3
s = util.Stack!
s\push 1
s\push 2
print "stack", s\size!, s\pop!, s\size!
print "same module", require("lib.util") == util
helper = require "lib.helper"
print helper.greet("moon"), helper.name
print "lua requiring moon", require("lib.uses_moon").sum 1, 2, 3
package.moonpath ..= ";./?/init.moon"
print "package init", require("pkg").kind
print "moonpath", package.moonpath == require("moonscript.base").create_moonpath(package.path)
print "moonpath entries end in .moon", not package.moonpath\find("%.lua;") and package.moonpath\match("%.moon$") != nil
ok, err = pcall require, "lib.bad_syntax"
print "bad syntax", ok, (err\gsub("^.-base%.lua:%d+: ", ""))
ok, err = pcall require, "lib.errors_on_load"
print "error on load", ok, err
ok, err = pcall require, "lib.errors_on_load"
print "second try", ok, err
print "loader installed", package.loaders[2] == require("moonscript.base").moon_loader
