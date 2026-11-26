my_env = {
  secret_function: -> print "shhh this is secret"
  say_hi: -> print "hi there!"
}

say_hi = -> print "I am a closure"

fn = ->
  secret_function!
  say_hi!

run_with_scope fn, my_env
