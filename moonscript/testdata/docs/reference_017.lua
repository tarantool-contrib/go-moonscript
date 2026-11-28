local send_message
send_message = function(_arg_0)
  local sender, recipient, body
  sender, recipient, body = _arg_0.sender, _arg_0.recipient, _arg_0.body
  return print(tostring(sender) .. " -> " .. tostring(recipient) .. ": " .. tostring(body))
end
return send_message({
  sender = "leaf",
  recipient = "world",
  body = "hello"
})