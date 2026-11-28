do
  local hello = os.getenv("hello")
  if hello then
    return print("You have hello", hello)
  else
    do
      local world = os.getenv("world")
      if world then
        return print("you have world", world)
      else
        return print("nothing :(")
      end
    end
  end
end