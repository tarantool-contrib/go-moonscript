-- run as `moon sub/nested.moon`: modules still resolve from the working directory
util = require "lib.util"
print "nested", util.add 20, 22
error "nested failure"
