local my_env = {
  secret_function = function()
    return print("shhh this is secret")
  end,
  say_hi = function()
    return print("hi there!")
  end
}
local say_hi
say_hi = function()
  return print("I am a closure")
end
local fn
fn = function()
  secret_function()
  return say_hi()
end
return run_with_scope(fn, my_env)