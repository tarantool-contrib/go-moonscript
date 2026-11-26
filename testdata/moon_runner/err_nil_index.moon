config = {
  server: { port: 80 }
}

get = (key) ->
  config.client.timeout

print "port", config.server.port
print get "timeout"
