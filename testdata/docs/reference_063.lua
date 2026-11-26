do
  local user = database.find_user("moon")
  if user then
    return print(user.name)
  end
end